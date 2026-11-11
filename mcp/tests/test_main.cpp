#define SLIMR_TEST_MAIN
#include "test.hpp"
#include "app.hpp"
#include "oauth_as.hpp"

#include <mcp_http.hpp>

#include <arpa/inet.h>
#include <atomic>
#include <chrono>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <map>
#include <netinet/in.h>
#include <string>
#include <sys/select.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <thread>
#include <unistd.h>
#include <vector>

using namespace whatsapp;

// ---------------- Mock Unix Socket Server ----------------

struct MockSocketServer {
    int server_fd = -1;
    std::filesystem::path sock_path;
    std::thread worker;
    std::atomic<bool> stop{false};
    std::string dl_path;
    std::atomic<bool> paired{false};
    std::atomic<bool> connected{true};
    std::string pair_jid{"15551234567@s.whatsapp.net"};
    std::string qr_status{"available"};
    std::string qr_payload{"2@mock-qr-payload-0123456789abcdef"};
    std::atomic<int> pair_status_calls{0};
    std::atomic<int> pair_qr_calls{0};
    std::atomic<int> pair_cancel_calls{0};
    std::atomic<int> send_calls{0};

    explicit MockSocketServer(const std::filesystem::path& p) : sock_path(p) {
        server_fd = socket(AF_UNIX, SOCK_STREAM, 0);
        struct sockaddr_un addr{};
        addr.sun_family = AF_UNIX;
        std::strncpy(addr.sun_path, sock_path.c_str(), sizeof(addr.sun_path) - 1);
        unlink(sock_path.c_str());
        int brc = bind(server_fd, reinterpret_cast<struct sockaddr*>(&addr), sizeof(addr));
        REQUIRE(brc == 0);
        int lrc = listen(server_fd, 16);
        REQUIRE(lrc == 0);

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
                        char buf[4096] = {0};
                        ssize_t n = read(client, buf, sizeof(buf) - 1);
                        std::string req(buf, n > 0 ? static_cast<size_t>(n) : 0);
                        std::string resp;
                        auto make_resp = [](int code, std::string_view statusText, std::string_view body) {
                            return "HTTP/1.1 " + std::to_string(code) + " " + std::string(statusText) +
                                   "\r\nContent-Type: application/json\r\nContent-Length: " +
                                   std::to_string(body.size()) + "\r\nConnection: close\r\n\r\n" +
                                   std::string(body);
                        };
                        if (req.find("GET /api/pair/status") != std::string::npos) {
                            pair_status_calls.fetch_add(1);
                            slimr::Json j = slimr::json_obj();
                            j["paired"] = paired.load();
                            j["connected"] = paired.load() && connected.load();
                            j["jid"] = paired.load() ? pair_jid : "";
                            resp = make_resp(200, "OK", slimr::write_json(j));
                        } else if (req.find("GET /api/pair/qr") != std::string::npos) {
                            pair_qr_calls.fetch_add(1);
                            slimr::Json j = slimr::json_obj();
                            if (paired.load()) {
                                j["status"] = "already_paired";
                                j["paired"] = true;
                                j["jid"] = pair_jid;
                            } else {
                                j["status"] = qr_status;
                                j["paired"] = false;
                                if (qr_status == "available") {
                                    j["qr"] = qr_payload;
                                }
                            }
                            resp = make_resp(200, "OK", slimr::write_json(j));
                        } else if (req.find("POST /api/pair/cancel") != std::string::npos) {
                            pair_cancel_calls.fetch_add(1);
                            if (paired.load()) {
                                resp = make_resp(409, "Conflict", R"({"success":false,"error":"already paired"})");
                            } else {
                                resp = make_resp(200, "OK", R"({"success":true})");
                            }
                        } else if (req.find("/api/send") != std::string::npos) {
                            send_calls.fetch_add(1);
                            resp = make_resp(200, "OK", R"({"success":true,"message":"sent successfully"})");
                        } else if (req.find("/api/download") != std::string::npos) {
                            std::string body = R"({"success":true,"path":")" + dl_path +
                                               R"(","media_type":"image","filename":"photo.jpg"})";
                            resp = make_resp(200, "OK", body);
                        } else {
                            resp = make_resp(404, "Not Found", R"({"success":false,"message":"not found"})");
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

// ---------------- Loopback HTTP Client & Test Helpers ----------------

namespace {

struct HttpTestResp {
    int status = 0;
    std::map<std::string, std::string> headers;
    std::vector<std::string> setCookies;
    std::string body;
};

inline std::string lower_ascii(std::string_view s) {
    std::string out(s);
    for (char& c : out) {
        if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
    }
    return out;
}

inline HttpTestResp http_request(uint16_t port,
                                 const std::string& method,
                                 const std::string& target,
                                 const std::vector<std::pair<std::string, std::string>>& headers = {},
                                 const std::string& body = {}) {
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    REQUIRE(fd >= 0);

    struct sockaddr_in addr{};
    addr.sin_family = AF_INET;
    addr.sin_port = htons(port);
    REQUIRE(inet_pton(AF_INET, "127.0.0.1", &addr.sin_addr) == 1);
    REQUIRE(connect(fd, reinterpret_cast<struct sockaddr*>(&addr), sizeof(addr)) == 0);

    std::string req = method + " " + target + " HTTP/1.1\r\n";
    req += "Host: 127.0.0.1:" + std::to_string(port) + "\r\n";
    req += "Connection: close\r\n";
    bool hasContentLen = false;
    for (const auto& [k, v] : headers) {
        if (lower_ascii(k) == "content-length") hasContentLen = true;
        req += k + ": " + v + "\r\n";
    }
    if (!body.empty() && !hasContentLen) {
        req += "Content-Length: " + std::to_string(body.size()) + "\r\n";
    }
    req += "\r\n";
    req += body;

    size_t sent = 0;
    while (sent < req.size()) {
        ssize_t n = write(fd, req.data() + sent, req.size() - sent);
        REQUIRE(n > 0);
        sent += static_cast<size_t>(n);
    }

    std::string raw;
    char buf[4096];
    for (;;) {
        ssize_t n = read(fd, buf, sizeof(buf));
        if (n <= 0) break;
        raw.append(buf, static_cast<size_t>(n));
    }
    close(fd);

    HttpTestResp resp;
    size_t hdrEnd = raw.find("\r\n\r\n");
    REQUIRE(hdrEnd != std::string::npos);
    std::string hdrBlock = raw.substr(0, hdrEnd);
    resp.body = raw.substr(hdrEnd + 4);

    size_t lineStart = 0;
    bool firstLine = true;
    while (lineStart < hdrBlock.size()) {
        size_t lineEnd = hdrBlock.find("\r\n", lineStart);
        if (lineEnd == std::string::npos) lineEnd = hdrBlock.size();
        std::string line = hdrBlock.substr(lineStart, lineEnd - lineStart);
        lineStart = lineEnd + 2;

        if (firstLine) {
            firstLine = false;
            size_t sp1 = line.find(' ');
            REQUIRE(sp1 != std::string::npos);
            resp.status = std::stoi(line.substr(sp1 + 1, 3));
            continue;
        }
        size_t colon = line.find(':');
        if (colon != std::string::npos) {
            std::string key = lower_ascii(line.substr(0, colon));
            std::string val = line.substr(colon + 1);
            size_t first = val.find_first_not_of(" \t");
            val = (first == std::string::npos) ? "" : val.substr(first);
            if (key == "set-cookie") {
                resp.setCookies.push_back(val);
            }
            resp.headers[key] = val;
        }
    }
    return resp;
}

inline std::string extract_hidden_input(const std::string& html, const std::string& name) {
    std::string needle = "name=\"" + name + "\" value=\"";
    size_t pos = html.find(needle);
    REQUIRE(pos != std::string::npos);
    pos += needle.size();
    size_t end = html.find('"', pos);
    REQUIRE(end != std::string::npos);
    return html.substr(pos, end - pos);
}

inline std::string extract_cookie_pair(const std::vector<std::string>& setCookies, const std::string& name) {
    std::string prefix = name + "=";
    for (const auto& sc : setCookies) {
        if (sc.rfind(prefix, 0) == 0) {
            size_t semi = sc.find(';');
            return (semi == std::string::npos) ? sc : sc.substr(0, semi);
        }
    }
    return "";
}

inline std::string extract_query_param(const std::string& url, const std::string& name) {
    size_t q = url.find('?');
    if (q == std::string::npos) return "";
    auto params = oauth_detail::parse_form_urlencoded(url.substr(q + 1));
    auto it = params.find(name);
    return (it == params.end()) ? "" : it->second;
}

struct ScopedEnvVar {
    std::string name;
    std::optional<std::string> prev;
    ScopedEnvVar(std::string k, const std::string& v) : name(std::move(k)) {
        if (const char* old = std::getenv(name.c_str())) prev = std::string(old);
        setenv(name.c_str(), v.c_str(), 1);
    }
    ~ScopedEnvVar() {
        if (prev.has_value()) {
            setenv(name.c_str(), prev->c_str(), 1);
        } else {
            unsetenv(name.c_str());
        }
    }
};

inline slimr::Json parse_json_ok(std::string_view s) {
    auto p = slimr::parse_json(s);
    REQUIRE(p.has_value());
    return *p;
}

inline void seed_test_messages_db(const std::filesystem::path& dbPath) {
    slimr::Database db(dbPath.string());
    db.exec(
        "CREATE TABLE chats (jid TEXT PRIMARY KEY, name TEXT, last_message_time TIMESTAMP);"
        "CREATE TABLE messages (id TEXT, chat_jid TEXT, sender TEXT, content TEXT, "
        "timestamp TIMESTAMP, is_from_me BOOLEAN, media_type TEXT, filename TEXT, "
        "url TEXT, media_key BLOB, file_sha256 BLOB, file_enc_sha256 BLOB, file_length INTEGER, "
        "PRIMARY KEY (id, chat_jid));"
        "INSERT INTO chats (jid, name, last_message_time) VALUES "
        "('15551234567@s.whatsapp.net', 'Alice', '2026-11-10 10:00:00 +0000 UTC');"
        "INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me) VALUES "
        "('m-http-1', '15551234567@s.whatsapp.net', '15551234567@s.whatsapp.net', "
        "'Hello from WhatsApp HTTP test', '2026-11-10 10:00:00 +0000 UTC', 0);"
    );
}

} // namespace

// ---------------- QR SVG Tests ----------------

TEST_CASE("qr_to_svg encoding and structure") {
    SUBCASE("small payload uses version 1 and valid finder patterns") {
        auto m = encode_qr_matrix("hello-qr");
        CHECK(m.size() == 21);
        // Top-left 7x7 finder outer border must be dark and inner ring light
        for (int i = 0; i < 7; ++i) {
            CHECK(m.get(i, 0));
            CHECK(m.get(0, i));
            CHECK(m.get(i, 6));
            CHECK(m.get(6, i));
        }
        CHECK(!m.get(1, 1));
        CHECK(m.get(3, 3));

        std::string svg = qr_to_svg("hello-qr");
        CHECK(svg.find("<svg xmlns=\"http://www.w3.org/2000/svg\"") != std::string::npos);
        CHECK(svg.find("viewBox=\"0 0 29 29\"") != std::string::npos);
        CHECK(svg.find("<rect width=\"100%\" height=\"100%\" fill=\"#ffffff\"/>") != std::string::npos);
        CHECK(svg.find("h1v1h-1z") != std::string::npos);
    }

    SUBCASE("long whatsmeow-like pairing payload uses multi-block EC-L cleanly") {
        std::string longPayload(210, 'A');
        longPayload.replace(0, 2, "2@");
        std::string svg = qr_to_svg(longPayload);
        CHECK(svg.rfind("<svg ", 0) == 0);
        CHECK(svg.find("</svg>") != std::string::npos);
        CHECK(encode_qr_matrix(longPayload).size() >= 45);
    }

    SUBCASE("oversized payload throws") {
        std::string tooBig(3500, 'Z');
        bool threw = false;
        try {
            (void)qr_to_svg(tooBig);
        } catch (const std::exception&) {
            threw = true;
        }
        CHECK(threw);
    }
}

// ---------------- HTTP + OAuth 2.1 + Browser QR/Approval Flow ----------------

TEST_CASE("http oauth 2.1 flow, qr pairing, approval, pkce, replay, scope, revocation") {
    auto tmpDir = std::filesystem::path("wa_http_oauth_test");
    std::filesystem::remove_all(tmpDir);
    std::filesystem::create_directories(tmpDir);
    auto cleanup = std::shared_ptr<void>(nullptr, [tmpDir](void*) {
        std::filesystem::remove_all(tmpDir);
    });

    ScopedEnvVar envDir("WHATSAPP_CLIENT_DATA_DIR", tmpDir.string());
    seed_test_messages_db(tmpDir / "messages.db");

    auto sockPath = tmpDir / "daemon.sock";
    MockSocketServer mock(sockPath);
    {
        auto st = daemon_pair_status(sockPath.string());
        INFO("daemon_pair_status error: " << st.error << " path=" << sockPath.string());
        REQUIRE(st.available);
    }

    auto app = make_whatsapp_app();
    slimr::HttpServerOptions httpOpts;
    httpOpts.host = "127.0.0.1";
    httpOpts.port = 0;
    httpOpts.allowEphemeralPort = true;
    auto oauthServer = configure_whatsapp_http_oauth(
        httpOpts,
        OAuthAsConfig{
            .daemonSocketPath = sockPath.string(),
            .defaultBaseUrl = "http://127.0.0.1:8088",
        },
        /*isFixedPort=*/false);

    slimr::McpHttpServer server(app, httpOpts);
    std::string startErr;
    REQUIRE(server.start(&startErr));
    auto stopServer = std::shared_ptr<void>(nullptr, [&server](void*) { server.stop(); });

    const uint16_t port = server.boundPort();
    REQUIRE(port > 0);
    const std::string origin = "http://127.0.0.1:" + std::to_string(port);
    const std::string resourceUrl = origin + "/mcp";

    // 1. Unauthenticated /mcp returns 401 + WWW-Authenticate
    {
        auto res = http_request(
            port,
            "POST",
            "/mcp",
            {{"Content-Type", "application/json"},
             {"Accept", "application/json, text/event-stream"}},
            R"({"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}})");
        CHECK(res.status == 401);
        auto it = res.headers.find("www-authenticate");
        REQUIRE(it != res.headers.end());
        CHECK(it->second.find("resource_metadata=\"" + origin + "/.well-known/oauth-protected-resource\"") != std::string::npos);
    }

    // 2. RFC 9728 Protected Resource Metadata
    {
        auto res = http_request(port, "GET", "/.well-known/oauth-protected-resource");
        REQUIRE(res.status == 200);
        auto j = parse_json_ok(res.body);
        CHECK(j["resource"].get_string() == resourceUrl);
        REQUIRE(j["authorization_servers"].get_array().size() == 1);
        CHECK(j["authorization_servers"][0].get_string() == origin);
    }

    // 3. RFC 8414 Authorization Server Metadata
    {
        auto res = http_request(port, "GET", "/.well-known/oauth-authorization-server");
        REQUIRE(res.status == 200);
        auto j = parse_json_ok(res.body);
        CHECK(j["issuer"].get_string() == origin);
        CHECK(j["authorization_endpoint"].get_string() == origin + "/authorize");
        CHECK(j["token_endpoint"].get_string() == origin + "/token");
        CHECK(j["registration_endpoint"].get_string() == origin + "/register");
        CHECK(j["revocation_endpoint"].get_string() == origin + "/revoke");
        CHECK(j["code_challenge_methods_supported"][0].get_string() == "S256");
    }

    // 4. DCR /register validates loopback redirect URIs
    {
        auto bad = http_request(
            port,
            "POST",
            "/register",
            {{"Content-Type", "application/json"}, {"Origin", origin}},
            R"({"client_name":"evil","redirect_uris":["https://evil.example/cb"]})");
        CHECK(bad.status == 400);
    }

    std::string clientId;
    const std::string redirectUri = "http://127.0.0.1:49152/callback";
    {
        auto reg = http_request(
            port,
            "POST",
            "/register",
            {{"Content-Type", "application/json"}, {"Origin", origin}},
            R"({"client_name":"Test Client","redirect_uris":[")" + redirectUri + R"("]})");
        REQUIRE(reg.status == 201);
        auto j = parse_json_ok(reg.body);
        clientId = j["client_id"].get_string();
        CHECK(!clientId.empty());
    }

    // 5. Unpaired browser /authorize renders QR UI (no silent auto-approve), transitions to paired, requires CSRF
    const std::string verifier1 = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk-test-verifier-1";
    const std::string challenge1 = oauth_detail::sha256_b64url(verifier1);
    std::string authCode1;
    {
        std::string authPath = "/authorize?response_type=code&client_id=" + oauth_detail::url_encode(clientId) +
                               "&redirect_uri=" + oauth_detail::url_encode(redirectUri) +
                               "&code_challenge=" + oauth_detail::url_encode(challenge1) +
                               "&code_challenge_method=S256" +
                               "&scope=" + oauth_detail::url_encode("whatsapp:read") +
                               "&resource=" + oauth_detail::url_encode(resourceUrl) +
                               "&state=state-unpaired";
        auto authPage = http_request(port, "GET", authPath);
        REQUIRE(authPage.status == 200);
        CHECK(authPage.body.find("/authorize/qr") != std::string::npos);
        CHECK(authPage.body.find("/authorize/approve") != std::string::npos);
        CHECK(authPage.body.find(mock.qr_payload) == std::string::npos); // never log/leak raw QR text in HTML

        std::string tx = extract_hidden_input(authPage.body, "tx");
        std::string csrf = extract_hidden_input(authPage.body, "csrf");
        std::string cookie = extract_cookie_pair(authPage.setCookies, "wa_oauth_csrf");
        REQUIRE(!cookie.empty());

        // Poll QR while unpaired
        auto qr1 = http_request(port, "GET", "/authorize/qr?tx=" + oauth_detail::url_encode(tx));
        REQUIRE(qr1.status == 200);
        auto qj1 = parse_json_ok(qr1.body);
        CHECK(qj1["paired"].get<bool>() == false);
        CHECK(qj1["status"].get_string() == "available");
        CHECK(!qj1["svg_b64"].get_string().empty());
        CHECK(qr1.body.find(mock.qr_payload) == std::string::npos);

        // Cannot approve while unpaired (must keep tx active and return 409)
        auto earlyApprove = http_request(
            port,
            "POST",
            "/authorize/approve",
            {{"Content-Type", "application/x-www-form-urlencoded"},
             {"Origin", origin},
             {"Cookie", cookie}},
            "tx=" + oauth_detail::url_encode(tx) + "&csrf=" + oauth_detail::url_encode(csrf) + "&action=approve");
        CHECK(earlyApprove.status == 409);

        // Pair completes on Go daemon
        mock.paired.store(true);
        auto qr2 = http_request(port, "GET", "/authorize/qr?tx=" + oauth_detail::url_encode(tx));
        REQUIRE(qr2.status == 200);
        auto qj2 = parse_json_ok(qr2.body);
        CHECK(qj2["paired"].get<bool>() == true);
        CHECK(qj2["jid"].get_string() == mock.pair_jid);

        // Reject missing/bad CSRF
        auto badCsrf = http_request(
            port,
            "POST",
            "/authorize/approve",
            {{"Content-Type", "application/x-www-form-urlencoded"},
             {"Origin", origin},
             {"Cookie", cookie}},
            "tx=" + oauth_detail::url_encode(tx) + "&csrf=wrong-csrf&action=approve");
        CHECK(badCsrf.status == 403);

        // Explicit browser approval redirects with single-use code + state
        auto approve = http_request(
            port,
            "POST",
            "/authorize/approve",
            {{"Content-Type", "application/x-www-form-urlencoded"},
             {"Origin", origin},
             {"Cookie", cookie}},
            "tx=" + oauth_detail::url_encode(tx) + "&csrf=" + oauth_detail::url_encode(csrf) + "&action=approve");
        REQUIRE(approve.status == 302);
        auto locIt = approve.headers.find("location");
        REQUIRE(locIt != approve.headers.end());
        CHECK(locIt->second.rfind(redirectUri + "?", 0) == 0);
        CHECK(extract_query_param(locIt->second, "state") == "state-unpaired");
        authCode1 = extract_query_param(locIt->second, "code");
        REQUIRE(!authCode1.empty());
    }

    // 6. PKCE S256 mismatch + wrong resource rejected (single-use consumes attempted code); valid /token succeeds; replay rejected
    std::string readToken;
    std::string refreshToken1;
    {
        auto issueCode = [&](const std::string& challenge) -> std::string {
            std::string path = "/authorize?response_type=code&client_id=" + oauth_detail::url_encode(clientId) +
                               "&redirect_uri=" + oauth_detail::url_encode(redirectUri) +
                               "&code_challenge=" + oauth_detail::url_encode(challenge) +
                               "&code_challenge_method=S256" +
                               "&scope=" + oauth_detail::url_encode("whatsapp:read") +
                               "&resource=" + oauth_detail::url_encode(resourceUrl) +
                               "&state=s-helper";
            auto pg = http_request(port, "GET", path);
            REQUIRE(pg.status == 200);
            std::string tx = extract_hidden_input(pg.body, "tx");
            std::string csrf = extract_hidden_input(pg.body, "csrf");
            std::string ck = extract_cookie_pair(pg.setCookies, "wa_oauth_csrf");
            auto ap = http_request(
                port,
                "POST",
                "/authorize/approve",
                {{"Content-Type", "application/x-www-form-urlencoded"},
                 {"Origin", origin},
                 {"Cookie", ck}},
                "tx=" + oauth_detail::url_encode(tx) + "&csrf=" + oauth_detail::url_encode(csrf) + "&action=approve");
            REQUIRE(ap.status == 302);
            return extract_query_param(ap.headers.at("location"), "code");
        };

        std::string codeBadPkce = issueCode(challenge1);
        auto badPkce = http_request(
            port,
            "POST",
            "/token",
            {{"Content-Type", "application/x-www-form-urlencoded"}, {"Origin", origin}},
            "grant_type=authorization_code&code=" + oauth_detail::url_encode(codeBadPkce) +
                "&redirect_uri=" + oauth_detail::url_encode(redirectUri) +
                "&client_id=" + oauth_detail::url_encode(clientId) +
                "&code_verifier=wrong_verifier_0123456789abcdefghijklmnopqrstuvwxyz" +
                "&resource=" + oauth_detail::url_encode(resourceUrl));
        CHECK(badPkce.status == 400);

        std::string codeBadResource = issueCode(challenge1);
        auto badResource = http_request(
            port,
            "POST",
            "/token",
            {{"Content-Type", "application/x-www-form-urlencoded"}, {"Origin", origin}},
            "grant_type=authorization_code&code=" + oauth_detail::url_encode(codeBadResource) +
                "&redirect_uri=" + oauth_detail::url_encode(redirectUri) +
                "&client_id=" + oauth_detail::url_encode(clientId) +
                "&code_verifier=" + oauth_detail::url_encode(verifier1) +
                "&resource=" + oauth_detail::url_encode("http://127.0.0.1:9999/mcp"));
        CHECK(badResource.status == 400);

        auto tokResp = http_request(
            port,
            "POST",
            "/token",
            {{"Content-Type", "application/x-www-form-urlencoded"}, {"Origin", origin}},
            "grant_type=authorization_code&code=" + oauth_detail::url_encode(authCode1) +
                "&redirect_uri=" + oauth_detail::url_encode(redirectUri) +
                "&client_id=" + oauth_detail::url_encode(clientId) +
                "&code_verifier=" + oauth_detail::url_encode(verifier1) +
                "&resource=" + oauth_detail::url_encode(resourceUrl));
        REQUIRE(tokResp.status == 200);
        auto tj = parse_json_ok(tokResp.body);
        readToken = tj["access_token"].get_string();
        refreshToken1 = tj["refresh_token"].get_string();
        CHECK(tj["scope"].get_string() == "whatsapp:read");
        REQUIRE(!readToken.empty());
        REQUIRE(!refreshToken1.empty());

        // Code replay must fail
        auto replay = http_request(
            port,
            "POST",
            "/token",
            {{"Content-Type", "application/x-www-form-urlencoded"}, {"Origin", origin}},
            "grant_type=authorization_code&code=" + oauth_detail::url_encode(authCode1) +
                "&redirect_uri=" + oauth_detail::url_encode(redirectUri) +
                "&client_id=" + oauth_detail::url_encode(clientId) +
                "&code_verifier=" + oauth_detail::url_encode(verifier1) +
                "&resource=" + oauth_detail::url_encode(resourceUrl));
        CHECK(replay.status == 400);
    }

    // 7. Authenticated /mcp calls: discover, read tool OK, write tool 403 insufficient_scope
    {
        auto disc = http_request(
            port,
            "POST",
            "/mcp",
            {{"Content-Type", "application/json"},
             {"Accept", "application/json, text/event-stream"},
             {"MCP-Protocol-Version", "2026-07-28"},
             {"Mcp-Method", "server/discover"},
             {"Authorization", "Bearer " + readToken}},
            R"({"jsonrpc":"2.0","id":2,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}})");
        REQUIRE(disc.status == 200);
        auto dj = parse_json_ok(disc.body);
        CHECK(dj["result"]["resultType"].get_string() == "complete");
        CHECK(dj["result"]["supportedVersions"][0].get_string() == "2026-07-28");
        CHECK(dj["result"]["_meta"]["io.modelcontextprotocol/serverInfo"]["name"].get_string() == "whatsapp-mcp");

        auto readCall = http_request(
            port,
            "POST",
            "/mcp",
            {{"Content-Type", "application/json"},
             {"Accept", "application/json, text/event-stream"},
             {"MCP-Protocol-Version", "2026-07-28"},
             {"Mcp-Method", "tools/call"},
             {"Mcp-Name", "list_chats"},
             {"Authorization", "Bearer " + readToken}},
            R"({"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_chats","arguments":{"limit":5},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}})");
        REQUIRE(readCall.status == 200);
        CHECK(readCall.body.find("15551234567@s.whatsapp.net") != std::string::npos);

        auto writeDenied = http_request(
            port,
            "POST",
            "/mcp",
            {{"Content-Type", "application/json"},
             {"Accept", "application/json, text/event-stream"},
             {"MCP-Protocol-Version", "2026-07-28"},
             {"Mcp-Method", "tools/call"},
             {"Mcp-Name", "send_message"},
             {"Authorization", "Bearer " + readToken}},
            R"({"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"send_message","arguments":{"recipient":"15551234567","message":"hi"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}})");
        CHECK(writeDenied.status == 403);
        auto wwwIt = writeDenied.headers.find("www-authenticate");
        REQUIRE(wwwIt != writeDenied.headers.end());
        CHECK(wwwIt->second.find("insufficient_scope") != std::string::npos);
    }

    // 8. Concurrent HTTP DB read calls under ASan/UBSan
    {
        std::vector<std::thread> workers;
        std::atomic<int> okCount{0};
        for (int t = 0; t < 4; ++t) {
            workers.emplace_back([&, t]() {
                for (int i = 0; i < 4; ++i) {
                    auto r = http_request(
                        port,
                        "POST",
                        "/mcp",
                        {{"Content-Type", "application/json"},
                         {"Accept", "application/json, text/event-stream"},
                         {"MCP-Protocol-Version", "2026-07-28"},
                         {"Mcp-Method", "tools/call"},
                         {"Mcp-Name", "list_chats"},
                         {"Authorization", "Bearer " + readToken}},
                        "{\"jsonrpc\":\"2.0\",\"id\":" + std::to_string(100 + t * 10 + i) +
                            ",\"method\":\"tools/call\",\"params\":{\"name\":\"list_chats\",\"arguments\":{\"limit\":5},\"_meta\":{\"io.modelcontextprotocol/protocolVersion\":\"2026-07-28\",\"io.modelcontextprotocol/clientCapabilities\":{}}}}");
                    if (r.status == 200 && r.body.find("Alice") != std::string::npos) {
                        okCount.fetch_add(1);
                    }
                }
            });
        }
        for (auto& w : workers) w.join();
        CHECK(okCount.load() == 16);
    }

    // 9. Already-paired /authorize NEVER triggers /api/pair/qr or /api/pair/cancel, supports write scope
    const int qrCallsBefore = mock.pair_qr_calls.load();
    const int cancelCallsBefore = mock.pair_cancel_calls.load();
    std::string writeToken;
    {
        const std::string verifier2 = "already-paired-verifier-0123456789abcdefghijklmnopqrstuvwxyz";
        const std::string challenge2 = oauth_detail::sha256_b64url(verifier2);
        std::string authPath = "/authorize?response_type=code&client_id=" + oauth_detail::url_encode(clientId) +
                               "&redirect_uri=" + oauth_detail::url_encode(redirectUri) +
                               "&code_challenge=" + oauth_detail::url_encode(challenge2) +
                               "&code_challenge_method=S256" +
                               "&scope=" + oauth_detail::url_encode("whatsapp:read whatsapp:write") +
                               "&resource=" + oauth_detail::url_encode(resourceUrl) +
                               "&state=state-paired";
        auto authPage = http_request(port, "GET", authPath);
        REQUIRE(authPage.status == 200);
        CHECK(authPage.body.find(mock.pair_jid) != std::string::npos);
        CHECK(authPage.body.find("Approve Connection") != std::string::npos);
        CHECK(mock.pair_qr_calls.load() == qrCallsBefore);

        std::string tx = extract_hidden_input(authPage.body, "tx");
        std::string csrf = extract_hidden_input(authPage.body, "csrf");
        std::string cookie = extract_cookie_pair(authPage.setCookies, "wa_oauth_csrf");
        auto approve = http_request(
            port,
            "POST",
            "/authorize/approve",
            {{"Content-Type", "application/x-www-form-urlencoded"},
             {"Origin", origin},
             {"Cookie", cookie}},
            "tx=" + oauth_detail::url_encode(tx) + "&csrf=" + oauth_detail::url_encode(csrf) + "&action=approve");
        REQUIRE(approve.status == 302);
        std::string code2 = extract_query_param(approve.headers.at("location"), "code");
        REQUIRE(!code2.empty());

        auto tokResp = http_request(
            port,
            "POST",
            "/token",
            {{"Content-Type", "application/x-www-form-urlencoded"}, {"Origin", origin}},
            "grant_type=authorization_code&code=" + oauth_detail::url_encode(code2) +
                "&redirect_uri=" + oauth_detail::url_encode(redirectUri) +
                "&client_id=" + oauth_detail::url_encode(clientId) +
                "&code_verifier=" + oauth_detail::url_encode(verifier2) +
                "&resource=" + oauth_detail::url_encode(resourceUrl));
        REQUIRE(tokResp.status == 200);
        auto tj = parse_json_ok(tokResp.body);
        writeToken = tj["access_token"].get_string();
        CHECK(tj["scope"].get_string() == "whatsapp:read whatsapp:write");
        CHECK(mock.pair_qr_calls.load() == qrCallsBefore);
        CHECK(mock.pair_cancel_calls.load() == cancelCallsBefore);

        auto sendCall = http_request(
            port,
            "POST",
            "/mcp",
            {{"Content-Type", "application/json"},
             {"Accept", "application/json, text/event-stream"},
             {"MCP-Protocol-Version", "2026-07-28"},
             {"Mcp-Method", "tools/call"},
             {"Mcp-Name", "send_message"},
             {"Authorization", "Bearer " + writeToken}},
            R"({"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"send_message","arguments":{"recipient":"15551234567","message":"ok"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}})");
        REQUIRE(sendCall.status == 200);
        CHECK(mock.send_calls.load() == 1);
    }

    // 10. Refresh token rotation + replay revokes family; /revoke does not disconnect daemon
    {
        auto refResp = http_request(
            port,
            "POST",
            "/token",
            {{"Content-Type", "application/x-www-form-urlencoded"}, {"Origin", origin}},
            "grant_type=refresh_token&refresh_token=" + oauth_detail::url_encode(refreshToken1) +
                "&client_id=" + oauth_detail::url_encode(clientId) +
                "&resource=" + oauth_detail::url_encode(resourceUrl));
        REQUIRE(refResp.status == 200);
        auto rj = parse_json_ok(refResp.body);
        std::string rotatedAccess = rj["access_token"].get_string();
        REQUIRE(!rotatedAccess.empty());

        // Replaying consumed refreshToken1 must fail AND revoke the rotated family
        auto replayRef = http_request(
            port,
            "POST",
            "/token",
            {{"Content-Type", "application/x-www-form-urlencoded"}, {"Origin", origin}},
            "grant_type=refresh_token&refresh_token=" + oauth_detail::url_encode(refreshToken1) +
                "&client_id=" + oauth_detail::url_encode(clientId) +
                "&resource=" + oauth_detail::url_encode(resourceUrl));
        CHECK(replayRef.status == 400);

        auto revokedUse = http_request(
            port,
            "POST",
            "/mcp",
            {{"Content-Type", "application/json"},
             {"Accept", "application/json, text/event-stream"},
             {"MCP-Protocol-Version", "2026-07-28"},
             {"Mcp-Method", "server/discover"},
             {"Authorization", "Bearer " + rotatedAccess}},
            R"({"jsonrpc":"2.0","id":6,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}})");
        CHECK(revokedUse.status == 401);

        // Explicit POST /revoke on writeToken revokes token without touching daemon pairing
        auto rev = http_request(
            port,
            "POST",
            "/revoke",
            {{"Content-Type", "application/x-www-form-urlencoded"}, {"Origin", origin}},
            "token=" + oauth_detail::url_encode(writeToken));
        REQUIRE(rev.status == 200);
        CHECK(mock.pair_cancel_calls.load() == cancelCallsBefore);
        CHECK(mock.paired.load() == true);

        auto afterRevoke = http_request(
            port,
            "POST",
            "/mcp",
            {{"Content-Type", "application/json"},
             {"Accept", "application/json, text/event-stream"},
             {"MCP-Protocol-Version", "2026-07-28"},
             {"Mcp-Method", "server/discover"},
             {"Authorization", "Bearer " + writeToken}},
            R"({"jsonrpc":"2.0","id":7,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}})");
        CHECK(afterRevoke.status == 401);
    }

    // 11. Refusal checks on enable_http host/port options
    {
        slimr::HttpServerOptions badHost;
        badHost.host = "0.0.0.0";
        badHost.port = 8088;
        slimr::McpHttpServer s(app, badHost);
        std::string err;
        CHECK(!s.start(&err));
    }
}

