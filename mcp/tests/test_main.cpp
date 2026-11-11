#define SLIMR_TEST_MAIN
#include "test.hpp"
#include "app.hpp"

#include <atomic>
#include <chrono>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <string>
#include <sys/select.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <thread>
#include <unistd.h>

using namespace whatsapp;

// ---------------- Mock Unix Socket Server ----------------

struct MockSocketServer {
    int server_fd = -1;
    std::filesystem::path sock_path;
    std::thread worker;
    std::atomic<bool> stop{false};
    std::string dl_path;

    explicit MockSocketServer(const std::filesystem::path& p) : sock_path(p) {
        server_fd = socket(AF_UNIX, SOCK_STREAM, 0);
        struct sockaddr_un addr{};
        addr.sun_family = AF_UNIX;
        std::strncpy(addr.sun_path, sock_path.c_str(), sizeof(addr.sun_path) - 1);
        unlink(sock_path.c_str());
        bind(server_fd, reinterpret_cast<struct sockaddr*>(&addr), sizeof(addr));
        listen(server_fd, 5);

        worker = std::thread([this]() {
            while (!stop.load()) {
                struct timeval tv{0, 20000}; // 20ms
                fd_set fds;
                FD_ZERO(&fds);
                FD_SET(server_fd, &fds);
                int sel = select(server_fd + 1, &fds, nullptr, nullptr, &tv);
                if (sel > 0) {
                    int client = accept(server_fd, nullptr, nullptr);
                    if (client >= 0) {
                        char buf[2048] = {0};
                        ssize_t n = read(client, buf, sizeof(buf) - 1);
                        std::string req(buf, n > 0 ? static_cast<size_t>(n) : 0);
                        std::string resp;
                        if (req.find("/api/send") != std::string::npos) {
                            std::string body = R"({"success":true,"message":"sent successfully"})";
                            resp = "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " +
                                   std::to_string(body.size()) + "\r\nConnection: close\r\n\r\n" + body;
                        } else if (req.find("/api/download") != std::string::npos) {
                            std::string body = R"({"success":true,"path":")" + dl_path +
                                               R"(","media_type":"image","filename":"photo.jpg"})";
                            resp = "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " +
                                   std::to_string(body.size()) + "\r\nConnection: close\r\n\r\n" + body;
                        } else {
                            std::string body = R"({"success":false,"message":"not found"})";
                            resp = "HTTP/1.1 404 Not Found\r\nContent-Length: " +
                                   std::to_string(body.size()) + "\r\nConnection: close\r\n\r\n" + body;
                        }
                        ssize_t ignored = write(client, resp.data(), resp.size());
                        (void)ignored;
                        close(client);
                    }
                }
            }
        });
    }

    ~MockSocketServer() {
        stop.store(true);
        if (worker.joinable()) worker.join();
        if (server_fd >= 0) close(server_fd);
        unlink(sock_path.c_str());
    }
};

// ---------------- wa_utc and time tests ----------------

TEST_CASE("wa_utc normalization") {
    SUBCASE("Go time.String with offset") {
        auto res = wa_utc_normalize("2026-11-08 00:40:38 +0100 IST");
        REQUIRE(res.has_value());
        CHECK(res.value() == "2026-11-07 23:40:38");
    }

    SUBCASE("RFC3339 UTC") {
        auto res = wa_utc_normalize("2026-11-08T00:40:38Z");
        REQUIRE(res.has_value());
        CHECK(res.value() == "2026-11-08 00:40:38");
    }

    SUBCASE("RFC3339 with positive offset") {
        auto res = wa_utc_normalize("2026-11-08T01:40:38+02:00");
        REQUIRE(res.has_value());
        CHECK(res.value() == "2026-11-07 23:40:38");
    }

    SUBCASE("SQLite datetime") {
        auto res = wa_utc_normalize("2026-11-08 12:30:00");
        REQUIRE(res.has_value());
        CHECK(res.value() == "2026-11-08 12:30:00");
    }

    SUBCASE("Invalid string") {
        auto res = wa_utc_normalize("not-a-datetime");
        CHECK_FALSE(res.has_value());
    }
}

TEST_CASE("path allowlist for send_file") {
    slimr::testing::TempDirGuard temp("wa_allow_");
    auto good_file = temp.path / "sample.pdf";
    {
        std::ofstream out(good_file);
        out << "Hello PDF content\n";
    }

    SUBCASE("valid absolute regular file") {
        CHECK_NOTHROW(validate_sendable_path(good_file.string()));
    }

    SUBCASE("reject relative path") {
        CHECK_THROWS_AS(validate_sendable_path("sample.pdf"), std::runtime_error);
    }

    SUBCASE("reject nonexistent file") {
        CHECK_THROWS_AS(validate_sendable_path((temp.path / "missing.txt").string()), std::runtime_error);
    }

    SUBCASE("reject hidden path") {
        auto hidden = temp.path / ".secret.txt";
        {
            std::ofstream out(hidden);
            out << "secret";
        }
        CHECK_THROWS_AS(validate_sendable_path(hidden.string()), std::runtime_error);
    }

    SUBCASE("reject path under ~/Library") {
        const char* home = std::getenv("HOME");
        if (home) {
            std::string lib_path = std::string(home) + "/Library/Preferences/com.apple.finder.plist";
            if (std::filesystem::exists(lib_path)) {
                CHECK_THROWS_AS(validate_sendable_path(lib_path), std::runtime_error);
            }
        }
    }
}

TEST_CASE("cursor encode/decode") {
    ExportCursor cur{
        .c = "15550000000@s.whatsapp.net",
        .t = "2026-11-08 14:00:00",
        .i = "ABCDEF123456"
    };

    auto enc = encode_cursor(cur);
    CHECK_FALSE(enc.empty());

    auto dec = decode_cursor(enc);
    REQUIRE(dec.has_value());
    CHECK(dec->c == cur.c);
    CHECK(dec->t == cur.t);
    CHECK(dec->i == cur.i);

    CHECK_FALSE(decode_cursor("invalid_base64_json!@#$").has_value());
}

// ---------------- SQLite database and session-key isolation tests ----------------

TEST_CASE("sqlite database, FTS5 and session-key isolation") {
    slimr::testing::TempDirGuard temp("wa_db_");

    // 1. Setup whatsapp.db with whatsmeow_device (sensitive) and contacts/lid_map
    {
        slimr::SqliteDb wdb((temp.path / "whatsapp.db").string());
        wdb.exec(R"(
            CREATE TABLE whatsmeow_device (
                jid TEXT PRIMARY KEY,
                registration_id INTEGER,
                noise_key BLOB,
                identity_key BLOB
            );
            INSERT INTO whatsmeow_device VALUES ('me@s.whatsapp.net', 9999, X'DEADBEEF', X'CAFEBABE');

            CREATE TABLE whatsmeow_contacts (
                our_jid TEXT,
                their_jid TEXT,
                first_name TEXT,
                full_name TEXT,
                push_name TEXT,
                business_name TEXT,
                redacted_phone TEXT,
                PRIMARY KEY (our_jid, their_jid)
            );
            INSERT INTO whatsmeow_contacts VALUES ('me@s.whatsapp.net', 'alice@s.whatsapp.net', 'Alice', 'Alice Smith', 'AliceS', NULL, NULL);
            INSERT INTO whatsmeow_contacts VALUES ('me@s.whatsapp.net', 'bob@s.whatsapp.net', 'Bob', 'Bob Jones', 'Bobby', NULL, NULL);

            CREATE TABLE whatsmeow_lid_map (
                lid TEXT PRIMARY KEY,
                pn TEXT UNIQUE NOT NULL
            );
            INSERT INTO whatsmeow_lid_map VALUES ('lid_alice', 'alice@s.whatsapp.net');
        )");
    }

    // 2. Setup messages.db with chats, messages, and messages_fts
    {
        slimr::SqliteDb mdb((temp.path / "messages.db").string());
        mdb.exec(R"(
            CREATE TABLE chats (
                jid TEXT PRIMARY KEY,
                name TEXT,
                last_message_time TIMESTAMP
            );
            INSERT INTO chats VALUES ('alice@s.whatsapp.net', 'Alice Smith', '2026-11-08 12:05:00');
            INSERT INTO chats VALUES ('group1@g.us', 'Secret Team', '2026-11-08 11:00:00');

            CREATE TABLE messages (
                id TEXT,
                chat_jid TEXT,
                sender TEXT,
                content TEXT,
                timestamp TIMESTAMP,
                is_from_me BOOLEAN,
                media_type TEXT,
                filename TEXT,
                PRIMARY KEY (id, chat_jid)
            );

            CREATE VIRTUAL TABLE messages_fts USING fts5(content, id UNINDEXED, chat_jid UNINDEXED);

            INSERT INTO messages VALUES ('m1', 'alice@s.whatsapp.net', 'alice@s.whatsapp.net', 'Hello from Alice, project is ready', '2026-11-08 12:00:00', 0, 'text', '');
            INSERT INTO messages_fts VALUES ('Hello from Alice, project is ready', 'm1', 'alice@s.whatsapp.net');

            INSERT INTO messages VALUES ('m2', 'alice@s.whatsapp.net', 'me@s.whatsapp.net', 'Great news Alice!', '2026-11-08 12:05:00', 1, 'text', '');
            INSERT INTO messages_fts VALUES ('Great news Alice!', 'm2', 'alice@s.whatsapp.net');

            INSERT INTO messages VALUES ('m3', 'group1@g.us', 'bob@s.whatsapp.net', 'Meeting in 10 minutes', '2026-11-08 11:00:00', 0, 'text', '');
            INSERT INTO messages_fts VALUES ('Meeting in 10 minutes', 'm3', 'group1@g.us');

            CREATE TABLE group_participants (
                group_jid TEXT,
                participant_jid TEXT,
                PRIMARY KEY (group_jid, participant_jid)
            );
            INSERT INTO group_participants VALUES ('group1@g.us', 'bob@s.whatsapp.net');
        )");
    }

    WhatsAppDb db(temp.path.string());

    SUBCASE("listChats") {
        auto chats = db.listChats(std::nullopt, 10, 0, true, "last_active");
        REQUIRE(chats.size() == 2);
        CHECK(chats[0].jid == "alice@s.whatsapp.net");
        CHECK(chats[0].name == "Alice Smith");
        CHECK(chats[0].last_message.has_value());
        CHECK(chats[0].last_message.value() == "Great news Alice!");

        auto filtered = db.listChats("Secret", 10, 0, true, "last_active");
        REQUIRE(filtered.size() == 1);
        CHECK(filtered[0].name == "Secret Team");
    }

    SUBCASE("listMessages with FTS5 search") {
        auto res = db.listMessages(std::nullopt, "project", std::nullopt, std::nullopt, std::nullopt, 10, 0);
        CHECK(res.find("Hello from Alice, project is ready") != std::string::npos);
    }

    SUBCASE("searchContacts") {
        auto contacts = db.searchContacts("Alice");
        REQUIRE(contacts.size() == 1);
        CHECK(contacts[0].jid == "alice@s.whatsapp.net");
        CHECK(contacts[0].name == "Alice Smith");
    }

    SUBCASE("getMessageContext") {
        auto ctx = db.getMessageContext("m2", 5, 5);
        CHECK(ctx.is_object());
        CHECK(ctx.contains("message"));
        CHECK(ctx["message"]["id"].get<std::string>() == "m2");
        CHECK(ctx["before"].is_array());
        CHECK(ctx["before"].get<slimr::Json::array_t>().size() == 1);
    }

    SUBCASE("getDirectChatByContact and getContactChats") {
        auto dc = db.getDirectChatByContact("alice");
        CHECK(dc.jid == "alice@s.whatsapp.net");

        auto cc = db.getContactChats("bob@s.whatsapp.net");
        REQUIRE(cc.size() == 1);
        CHECK(cc[0].jid == "group1@g.us");
    }

    SUBCASE("getLastInteraction") {
        auto last = db.getLastInteraction("alice@s.whatsapp.net");
        CHECK(last.find("Alice") != std::string::npos);
    }

    SUBCASE("exportMessages") {
        auto page = db.exportMessages("2026-11-08 00:00:00", "2026-11-08 23:59:59", {}, "", 10);
        REQUIRE(page.messages.size() == 3);
        CHECK(page.next_cursor.empty());

        auto lines = format_export_lines(page);
        CHECK(lines.find("Alice Smith") != std::string::npos);
        CHECK(lines.find("Hello from Alice") != std::string::npos);
    }

    SUBCASE("session-key isolation: whatsmeow_device is inaccessible") {
        // Querying v_messages and v_contacts works
        auto msgRes = db.querySql("SELECT COUNT(*) AS total FROM v_messages", 10);
        REQUIRE(msgRes.rows.size() == 1);
        CHECK(msgRes.rows[0]["total"].get<int64_t>() == 3);

        auto conRes = db.querySql("SELECT jid, full_name FROM v_contacts WHERE jid = 'alice@s.whatsapp.net'", 10);
        REQUIRE(conRes.rows.size() == 1);
        CHECK(conRes.rows[0]["full_name"].get<std::string>() == "Alice Smith");

        // whatsmeow_device was detached and MUST NOT be accessible!
        CHECK_THROWS_WITH_AS(
            db.querySql("SELECT * FROM whatsmeow_device", 10),
            doctest::Contains("no such table: whatsmeow_device"),
            std::runtime_error
        );

        // wasrc was detached and cannot be queried
        CHECK_THROWS_WITH_AS(
            db.querySql("SELECT * FROM wasrc.whatsmeow_device", 10),
            doctest::Contains("no such table"),
            std::runtime_error
        );
    }

    SUBCASE("PRAGMA query_only blocks all writes") {
        CHECK_THROWS(db.querySql("INSERT INTO chats VALUES ('bad@s.whatsapp.net', 'Hacked', '2026-01-01')", 10));
        CHECK_THROWS(db.querySql("UPDATE chats SET name = 'Hacked' WHERE jid = 'alice@s.whatsapp.net'", 10));
        CHECK_THROWS(db.querySql("DELETE FROM chats WHERE jid = 'alice@s.whatsapp.net'", 10));
        CHECK_THROWS(db.querySql("DROP TABLE chats", 10));
    }
}

// ---------------- Mock Unix Socket Tests ----------------

TEST_CASE("mock unix socket send and download") {
    slimr::testing::TempDirGuard temp("wa_sock_");
    // Unix socket addresses have a small fixed bound, even in deeply nested checkouts.
    auto sock_path = std::filesystem::relative(temp.path / "daemon.sock");
    auto dl_dir = temp.path / "downloads";
    std::filesystem::create_directories(dl_dir);

    auto img_file = dl_dir / "photo.jpg";
    {
        std::ofstream out(img_file, std::ios::binary);
        out << "fake image bytes";
    }

    MockSocketServer server(sock_path);
    server.dl_path = img_file.string();

    SUBCASE("daemon_send") {
        auto res = daemon_send(sock_path.string(), "friend@s.whatsapp.net", "hello world");
        CHECK(res.success);
        CHECK(res.message == "sent successfully");
    }

    SUBCASE("daemon_download valid path inside downloads") {
        auto dl = daemon_download(sock_path.string(), "m123", "friend@s.whatsapp.net", temp.path.string());
        CHECK(dl.path == img_file.string());
        CHECK(dl.mediaType == "image");
        CHECK(dl.filename == "photo.jpg");
    }

    SUBCASE("daemon_download rejects path traversal outside downloads") {
        server.dl_path = (temp.path / "outside.txt").string();
        {
            std::ofstream out(server.dl_path);
            out << "evil outside";
        }
        CHECK_THROWS_WITH_AS(
            daemon_download(sock_path.string(), "m123", "friend@s.whatsapp.net", temp.path.string()),
            doctest::Contains("outside downloads directory"),
            std::runtime_error
        );
    }
}

// ---------------- MCP Plugin App Test ----------------

TEST_CASE("mcp app initialization and schema tool") {
    auto app = make_whatsapp_app();
    CHECK(app.key == "whatsapp-mcp");
    CHECK(app.description.find("WhatsApp MCP server") != std::string::npos);
    CHECK(app.instructions.find("WhatsApp for the MCP owner") != std::string::npos);

    // Verify all tools are registered
    CHECK(app.tools().contains("schema"));
    CHECK(app.tools().contains("list_chats"));
    CHECK(app.tools().contains("list_messages"));
    CHECK(app.tools().contains("search_contacts"));
    CHECK(app.tools().contains("get_chat"));
    CHECK(app.tools().contains("get_direct_chat_by_contact"));
    CHECK(app.tools().contains("get_contact_chats"));
    CHECK(app.tools().contains("get_message_context"));
    CHECK(app.tools().contains("get_last_interaction"));
    CHECK(app.tools().contains("send_message"));
    CHECK(app.tools().contains("send_file"));
    CHECK(app.tools().contains("send_audio_message"));
    CHECK(app.tools().contains("download_media"));
    CHECK(app.tools().contains("export"));
    CHECK(app.tools().contains("sql"));
    CHECK(app.tools().contains("status"));
}
