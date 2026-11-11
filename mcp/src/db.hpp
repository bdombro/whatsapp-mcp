#pragma once

#include "allowlist.hpp"
#include "audit.hpp"
#include "daemon_client.hpp"
#include "schemas.hpp"
#include "time_util.hpp"

#include "json.hpp"
#include "sqlite.hpp"

#include <algorithm>
#include <cctype>
#include <cstdlib>
#include <filesystem>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <sstream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace whatsapp {

inline std::mutex& db_mutex_for(const std::string& path) {
    static std::mutex registryMu;
    static std::map<std::string, std::unique_ptr<std::mutex>> registry;
    std::lock_guard<std::mutex> lk(registryMu);
    auto& ptr = registry[path];
    if (!ptr) {
        ptr = std::make_unique<std::mutex>();
    }
    return *ptr;
}

inline std::string base64_url_encode(std::string_view input) {
    static const char tbl[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
    std::string out;
    size_t val = 0;
    int valb = -6;
    for (uint8_t c : input) {
        val = (val << 8) + c;
        valb += 8;
        while (valb >= 0) {
            out.push_back(tbl[(val >> valb) & 0x3F]);
            valb -= 6;
        }
    }
    if (valb > -6) {
        out.push_back(tbl[((val << 8) >> (valb + 8)) & 0x3F]);
    }
    return out;
}

inline std::optional<std::string> base64_url_decode(std::string_view input) {
    auto decode_char = [](char c) -> int {
        if (c >= 'A' && c <= 'Z') return c - 'A';
        if (c >= 'a' && c <= 'z') return c - 'a' + 26;
        if (c >= '0' && c <= '9') return c - '0' + 52;
        if (c == '-' || c == '+') return 62;
        if (c == '_' || c == '/') return 63;
        return -1;
    };
    std::string out;
    int val = 0;
    int valb = -8;
    for (char c : input) {
        if (c == '=') break;
        int d = decode_char(c);
        if (d == -1) return std::nullopt;
        val = (val << 6) + d;
        valb += 6;
        if (valb >= 0) {
            out.push_back(static_cast<char>((val >> valb) & 0xFF));
            valb -= 8;
        }
    }
    return out;
}

template <typename T>
inline T json_val(const slimr::Json& j, std::string_view key, T def = {}) {
    if (!j.is_object() || !j.contains(key)) return def;
    const auto& v = j[key];
    if constexpr (std::is_same_v<T, std::string>) {
        return v.is_string() ? v.get<std::string>() : def;
    } else if constexpr (std::is_integral_v<T>) {
        if (v.is_number()) return static_cast<T>(v.get<int64_t>());
        return def;
    } else if constexpr (std::is_same_v<T, bool>) {
        if (v.is_boolean()) return v.get<bool>();
        if (v.is_number()) return v.get<int64_t>() != 0;
        return def;
    } else {
        return def;
    }
}

struct ExportCursor {
    std::string c;
    std::string t;
    std::string i;

    std::string chat_jid() const { return c; }
    std::string timestamp() const { return t; }
    std::string id() const { return i; }
};

inline std::string encode_export_cursor(const std::string& chatJid, const std::string& timeUtc, const std::string& id) {
    slimr::Json j = slimr::Json::object_t{};
    j["c"] = chatJid;
    j["t"] = timeUtc;
    j["i"] = id;
    return base64_url_encode(slimr::write_json(j));
}

inline std::string encode_cursor(const ExportCursor& cur) {
    return encode_export_cursor(cur.c, cur.t, cur.i);
}

inline std::optional<ExportCursor> decode_export_cursor(const std::string& cursorStr) {
    auto decoded = base64_url_decode(cursorStr);
    if (!decoded) return std::nullopt;
    try {
        auto jRes = slimr::parse_json(*decoded);
        if (!jRes) return std::nullopt;
        const auto& j = *jRes;
        if (!j.is_object() || !j.contains("c") || !j.contains("t") || !j.contains("i")) {
            return std::nullopt;
        }
        return ExportCursor{
            j["c"].is_string() ? j["c"].get<std::string>() : "",
            j["t"].is_string() ? j["t"].get<std::string>() : "",
            j["i"].is_string() ? j["i"].get<std::string>() : ""
        };
    } catch (...) {
        return std::nullopt;
    }
}

inline std::optional<ExportCursor> decode_cursor(const std::string& cursorStr) {
    return decode_export_cursor(cursorStr);
}

inline std::string build_setup_sql(const std::string& waPath) {
    std::string sql;
    if (!waPath.empty() && std::filesystem::exists(waPath)) {
        std::string escapedWa = waPath;
        size_t pos = 0;
        while ((pos = escapedWa.find('\'', pos)) != std::string::npos) {
            escapedWa.replace(pos, 1, "''");
            pos += 2;
        }
        sql += "ATTACH DATABASE '" + escapedWa + "' AS wasrc;\n";
        sql += "CREATE TEMP TABLE t_contacts (our_jid TEXT, their_jid TEXT, first_name TEXT, full_name TEXT, push_name TEXT, business_name TEXT, redacted_phone TEXT);\n";
        sql += "INSERT OR IGNORE INTO t_contacts SELECT our_jid, their_jid, first_name, full_name, push_name, business_name, redacted_phone FROM wasrc.whatsmeow_contacts;\n";
        sql += "CREATE TEMP TABLE t_lid_map (lid TEXT PRIMARY KEY, pn TEXT UNIQUE NOT NULL);\n";
        sql += "INSERT OR IGNORE INTO t_lid_map SELECT lid, pn FROM wasrc.whatsmeow_lid_map;\n";
        sql += "DETACH DATABASE wasrc;\n";
    } else {
        sql += "CREATE TEMP TABLE t_contacts (our_jid TEXT, their_jid TEXT, first_name TEXT, full_name TEXT, push_name TEXT, business_name TEXT, redacted_phone TEXT);\n";
        sql += "CREATE TEMP TABLE t_lid_map (lid TEXT PRIMARY KEY, pn TEXT UNIQUE NOT NULL);\n";
    }
    sql += "CREATE TEMP TABLE t_names (user TEXT PRIMARY KEY, name TEXT NOT NULL);\n";
    sql += "INSERT OR IGNORE INTO t_names SELECT substr(jid, 1, instr(jid, '@') - 1), name FROM main.chats WHERE jid NOT LIKE '%@g.us' AND instr(jid, '@') > 0 AND COALESCE(name, '') <> '' AND name NOT GLOB '[0-9+]*';\n";
    sql += "INSERT OR IGNORE INTO t_names SELECT substr(their_jid, 1, instr(their_jid, '@') - 1), COALESCE(NULLIF(full_name, ''), NULLIF(push_name, ''), NULLIF(business_name, '')) FROM t_contacts WHERE their_jid NOT LIKE '%@g.us' AND instr(their_jid, '@') > 0 AND COALESCE(NULLIF(full_name, ''), NULLIF(push_name, ''), NULLIF(business_name, '')) IS NOT NULL;\n";
    sql += "INSERT OR IGNORE INTO t_names SELECT l.lid, n.name FROM t_lid_map l JOIN t_names n ON n.user = l.pn;\n";
    sql += "INSERT OR IGNORE INTO t_names SELECT l.pn, n.name FROM t_lid_map l JOIN t_names n ON n.user = l.lid;\n";
    sql += R"(
CREATE TEMP VIEW IF NOT EXISTS v_messages AS
    SELECT m.rowid AS rowid,
        m.id AS id,
        m.chat_jid AS chat_jid,
        c.name AS chat_name,
        (m.chat_jid LIKE '%@g.us') AS is_group,
        m.sender AS sender,
        CASE WHEN m.is_from_me THEN 'Me' ELSE COALESCE(n.name, m.sender) END AS sender_name,
        m.is_from_me AS is_from_me,
        wa_utc(m.timestamp) AS ts_utc,
        substr(m.timestamp, 1, 19) AS ts_local,
        m.content AS content,
        NULLIF(m.media_type, '') AS media_type,
        NULLIF(m.filename, '') AS filename
    FROM main.messages m
    LEFT JOIN main.chats c ON c.jid = m.chat_jid
    LEFT JOIN t_names n ON n.user = m.sender;

CREATE TEMP VIEW IF NOT EXISTS v_chats AS
    SELECT c.jid AS jid,
        c.name AS name,
        (c.jid LIKE '%@g.us') AS is_group,
        wa_utc(c.last_message_time) AS last_message_utc,
        COALESCE(mc.n, 0) AS message_count
    FROM main.chats c
    LEFT JOIN (SELECT chat_jid, COUNT(*) AS n FROM main.messages GROUP BY chat_jid) mc ON mc.chat_jid = c.jid;

CREATE TEMP VIEW IF NOT EXISTS v_contacts AS
    SELECT k.their_jid AS jid,
        CASE WHEN k.their_jid LIKE '%@lid'
            THEN (SELECT pn FROM t_lid_map WHERE lid = substr(k.their_jid, 1, instr(k.their_jid, '@') - 1))
            ELSE substr(k.their_jid, 1, instr(k.their_jid, '@') - 1) END AS phone,
        CASE WHEN k.their_jid LIKE '%@lid'
            THEN substr(k.their_jid, 1, instr(k.their_jid, '@') - 1)
            ELSE (SELECT lid FROM t_lid_map WHERE pn = substr(k.their_jid, 1, instr(k.their_jid, '@') - 1)) END AS lid,
        COALESCE(NULLIF(k.full_name, ''), NULLIF(k.push_name, ''), NULLIF(k.business_name, '')) AS name,
        NULLIF(k.full_name, '') AS full_name,
        NULLIF(k.push_name, '') AS push_name,
        NULLIF(k.business_name, '') AS business_name
    FROM t_contacts k
    WHERE k.their_jid NOT LIKE '%@g.us';
)";
    return sql;
}

inline std::string validate_read_only_sql(std::string_view sql) {
    while (!sql.empty() && std::isspace(static_cast<unsigned char>(sql.front()))) sql.remove_prefix(1);
    while (!sql.empty() && (std::isspace(static_cast<unsigned char>(sql.back())) || sql.back() == ';')) sql.remove_suffix(1);
    while (!sql.empty() && std::isspace(static_cast<unsigned char>(sql.back()))) sql.remove_suffix(1);
    if (sql.empty()) throw std::runtime_error("query is empty");

    std::string_view body = sql;
    while (true) {
        while (!body.empty() && std::isspace(static_cast<unsigned char>(body.front()))) body.remove_prefix(1);
        if (body.starts_with("--")) {
            auto pos = body.find('\n');
            if (pos == std::string_view::npos) throw std::runtime_error("query is empty");
            body.remove_prefix(pos + 1);
        } else if (body.starts_with("/*")) {
            auto pos = body.find("*/");
            if (pos == std::string_view::npos) throw std::runtime_error("query is empty");
            body.remove_prefix(pos + 2);
        } else {
            break;
        }
    }
    if (body.empty()) throw std::runtime_error("query is empty");

    std::string first;
    size_t i = 0;
    while (i < body.size() && !std::isspace(static_cast<unsigned char>(body[i]))) {
        first += static_cast<char>(std::toupper(static_cast<unsigned char>(body[i])));
        i++;
    }
    if (first != "SELECT" && first != "WITH") {
        throw std::runtime_error("only SELECT or WITH queries are allowed");
    }
    if (sql.find(';') != std::string_view::npos) {
        throw std::runtime_error("only one statement is allowed (remove ';')");
    }
    return std::string(sql);
}

struct MessageRow {
    std::string timestamp;
    std::string sender;
    std::string chat_name;
    std::string content;
    bool is_from_me = false;
    std::string chat_jid;
    std::string id;
    std::string media_type;
};

struct ExportMessage {
    std::string id;
    std::string chat_jid;
    std::string chat_name;
    std::string sender;
    std::string sender_name;
    bool is_from_me = false;
    std::string ts_utc;
    std::string content;
    std::string media_type;
    std::string filename;
};

struct ExportPage {
    std::vector<ExportMessage> messages;
    std::string next_cursor;
};

struct ChatRow {
    std::string jid;
    std::string name;
    bool is_group = false;
    std::optional<std::string> last_message_time;
    std::optional<std::string> last_message;
    std::optional<std::string> last_sender;
    std::optional<bool> last_is_from_me;
    int message_count = 0;
};

struct ContactRow {
    std::string phone_number;
    std::string name;
    std::string jid;
};

inline std::string format_message(const MessageRow& msg, const std::string& resolvedSender = "") {
    std::string out;
    std::string ts = msg.timestamp.size() >= 19 ? msg.timestamp.substr(0, 19) : msg.timestamp;
    if (!msg.chat_name.empty()) {
        out += "[" + ts + "] Chat: " + msg.chat_name + " ";
    } else {
        out += "[" + ts + "] ";
    }
    std::string prefix;
    if (!msg.media_type.empty()) {
        prefix = "[" + msg.media_type + " - Message ID: " + msg.id + " - Chat JID: " + msg.chat_jid + "] ";
    }
    std::string sender = msg.is_from_me ? "Me" : (resolvedSender.empty() ? msg.sender : resolvedSender);
    out += "From: " + sender + ": " + prefix + msg.content + "\n";
    return out;
}

inline std::string format_export_lines(const ExportPage& page) {
    if (page.messages.empty()) {
        return "No messages in range.\n";
    }
    std::string out;
    std::string currentChat;
    for (const auto& m : page.messages) {
        if (m.chat_jid != currentChat) {
            if (!currentChat.empty()) out += "\n";
            std::string name = m.chat_name.empty() ? m.chat_jid : m.chat_name;
            out += "# " + name + " [" + m.chat_jid + "]\n";
            currentChat = m.chat_jid;
        }
        std::string ts = utc_to_local_minute(m.ts_utc);
        std::string text = m.content;
        size_t p = 0;
        while ((p = text.find('\n', p)) != std::string::npos) {
            text.replace(p, 1, " ⏎ ");
            p += 4;
        }
        std::string collapsed;
        bool inSpace = false;
        for (char c : text) {
            if (std::isspace(static_cast<unsigned char>(c))) {
                if (!inSpace) { collapsed.push_back(' '); inSpace = true; }
            } else {
                collapsed.push_back(c);
                inSpace = false;
            }
        }
        while (!collapsed.empty() && collapsed.front() == ' ') collapsed.erase(0, 1);
        while (!collapsed.empty() && collapsed.back() == ' ') collapsed.pop_back();

        if (!m.media_type.empty()) {
            std::string media = "[" + m.media_type;
            if (!m.filename.empty()) media += ": " + m.filename;
            media += "]";
            if (collapsed.empty()) collapsed = media;
            else collapsed = media + " " + collapsed;
        }
        out += ts + " | " + m.sender_name + " | " + collapsed + " [id=" + m.id + "]\n";
    }
    if (!page.next_cursor.empty()) {
        out += "\n(more: pass cursor=" + page.next_cursor + ")\n";
    }
    return out;
}

class WhatsAppDb {
public:
    explicit WhatsAppDb(std::string dataDir) : dataDir_(std::move(dataDir)) {
        if (dataDir_ != ":memory:") {
            msgPath_ = dataDir_ + "/messages.db";
            waPath_ = dataDir_ + "/whatsapp.db";
        } else {
            msgPath_ = ":memory:";
            waPath_ = "";
        }
    }

    const std::string& dataDir() const noexcept { return dataDir_; }
    const std::string& msgPath() const noexcept { return msgPath_; }
    const std::string& waPath() const noexcept { return waPath_; }

    void ensureRegistered(slimr::SqliteDb& db) const {
        db.registerFunction("wa_utc", [](const std::vector<slimr::SqlValue>& args) -> slimr::SqlValue {
            if (args.empty()) return std::monostate{};
            if (std::holds_alternative<std::monostate>(args[0])) return std::monostate{};
            std::string s;
            if (std::holds_alternative<std::string>(args[0])) {
                s = std::get<std::string>(args[0]);
            } else if (std::holds_alternative<int64_t>(args[0])) {
                s = std::to_string(std::get<int64_t>(args[0]));
            }
            auto norm = wa_utc_normalize(s);
            if (!norm) return std::monostate{};
            return *norm;
        }, true);
    }

    std::unique_ptr<slimr::SqliteDb> openDb() const {
        auto db = std::make_unique<slimr::SqliteDb>(msgPath_, slimr::SqliteOptions{.readOnly = (msgPath_ != ":memory:")});
        ensureRegistered(*db);
        return db;
    }

    slimr::IterateResult querySql(const std::string& query, size_t limit = 200) const {
        std::string validated = validate_read_only_sql(query);
        std::lock_guard<std::mutex> lock(db_mutex_for(msgPath_));
        auto db = openDb();
        slimr::IterateOptions opts;
        opts.limit = (limit > 5000) ? 5000 : (limit == 0 ? 200 : limit);
        opts.readOnlyConnection = (msgPath_ != ":memory:");
        opts.setupSql = build_setup_sql(waPath_);
        return db->iterateLimited(validated, opts);
    }

    ExportPage exportMessages(const std::string& since,
                              const std::optional<std::string>& until = std::nullopt,
                              const std::vector<std::string>& chatJids = {},
                              const std::string& cursor = "",
                              int limit = 500) const {
        if (since.empty()) throw std::runtime_error("since is required");
        std::string sinceUtc = parse_date_bound(since, false);
        std::string untilUtc;
        if (until && !until->empty()) {
            untilUtc = parse_date_bound(*until, true);
            if (untilUtc <= sinceUtc) {
                throw std::runtime_error("until must be after since");
            }
        }
        if (limit <= 0) limit = 500;
        if (limit > 5000) limit = 5000;

        std::string sql = "SELECT id, chat_jid, COALESCE(chat_name, ''), sender, sender_name, is_from_me, ts_utc, "
                          "COALESCE(content, ''), COALESCE(media_type, ''), COALESCE(filename, '') FROM v_messages WHERE ";
        std::vector<std::string> where = {"ts_utc >= ?"};
        std::vector<slimr::SqlValue> params = {sinceUtc};

        if (!untilUtc.empty()) {
            where.push_back("ts_utc < ?");
            params.push_back(untilUtc);
        }

        if (!chatJids.empty()) {
            std::string ph = "chat_jid IN (";
            for (size_t i = 0; i < chatJids.size(); ++i) {
                if (i > 0) ph += ",";
                ph += "?";
                params.push_back(chatJids[i]);
            }
            ph += ")";
            where.push_back(ph);
        }

        if (!cursor.empty()) {
            auto cur = decode_export_cursor(cursor);
            if (!cur) throw std::runtime_error("invalid cursor");
            where.push_back("(chat_jid, ts_utc, id) > (?, ?, ?)");
            params.push_back(cur->c);
            params.push_back(cur->t);
            params.push_back(cur->i);
        }

        for (size_t i = 0; i < where.size(); ++i) {
            if (i > 0) sql += " AND ";
            sql += where[i];
        }
        sql += " ORDER BY chat_jid, ts_utc, id";

        std::lock_guard<std::mutex> lock(db_mutex_for(msgPath_));
        auto db = openDb();
        slimr::IterateOptions opts;
        opts.limit = static_cast<size_t>(limit + 1);
        opts.readOnlyConnection = (msgPath_ != ":memory:");
        opts.setupSql = build_setup_sql(waPath_);
        auto res = db->iterateLimited(sql, params, opts);

        ExportPage page;
        for (const auto& row : res.rows) {
            ExportMessage m;
            if (row.contains("id")) m.id = row["id"].is_string() ? row["id"].get<std::string>() : "";
            if (row.contains("chat_jid")) m.chat_jid = row["chat_jid"].is_string() ? row["chat_jid"].get<std::string>() : "";
            if (row.contains("COALESCE(chat_name, '')")) m.chat_name = row["COALESCE(chat_name, '')"].is_string() ? row["COALESCE(chat_name, '')"].get<std::string>() : "";
            if (row.contains("sender")) m.sender = row["sender"].is_string() ? row["sender"].get<std::string>() : "";
            if (row.contains("sender_name")) m.sender_name = row["sender_name"].is_string() ? row["sender_name"].get<std::string>() : "";
            m.is_from_me = json_val<bool>(row, "is_from_me");
            if (row.contains("ts_utc")) m.ts_utc = row["ts_utc"].is_string() ? row["ts_utc"].get<std::string>() : "";
            if (row.contains("COALESCE(content, '')")) m.content = row["COALESCE(content, '')"].is_string() ? row["COALESCE(content, '')"].get<std::string>() : "";
            if (row.contains("COALESCE(media_type, '')")) m.media_type = row["COALESCE(media_type, '')"].is_string() ? row["COALESCE(media_type, '')"].get<std::string>() : "";
            if (row.contains("COALESCE(filename, '')")) m.filename = row["COALESCE(filename, '')"].is_string() ? row["COALESCE(filename, '')"].get<std::string>() : "";
            page.messages.push_back(std::move(m));
        }

        if (static_cast<int>(page.messages.size()) > limit) {
            page.messages.resize(limit);
            const auto& last = page.messages.back();
            page.next_cursor = encode_export_cursor(last.chat_jid, last.ts_utc, last.id);
        }

        return page;
    }

    std::vector<ChatRow> listChats(const std::optional<std::string>& query = std::nullopt,
                                   int limit = 20, int page = 0,
                                   bool includeLast = true,
                                   const std::string& sortBy = "last_active") const {
        if (limit <= 0) limit = 20;
        if (page < 0) page = 0;
        int offset = page * limit;

        std::string sql;
        if (includeLast) {
            sql = "SELECT c.jid, c.name, (c.jid LIKE '%@g.us') AS is_group, c.last_message_time, "
                  "m.content, m.sender, m.is_from_me "
                  "FROM chats c "
                  "LEFT JOIN messages m ON c.jid = m.chat_jid AND c.last_message_time = m.timestamp ";
        } else {
            sql = "SELECT c.jid, c.name, (c.jid LIKE '%@g.us') AS is_group, c.last_message_time, "
                  "NULL AS content, NULL AS sender, NULL AS is_from_me FROM chats c ";
        }

        std::vector<slimr::SqlValue> params;
        if (query && !query->empty()) {
            sql += "WHERE (LOWER(c.name) LIKE LOWER(?) OR LOWER(c.jid) LIKE LOWER(?) OR c.jid IN ("
                   "SELECT group_jid FROM group_participants WHERE participant_jid IN ("
                   "SELECT their_jid FROM t_contacts WHERE LOWER(full_name) LIKE LOWER(?) OR LOWER(push_name) LIKE LOWER(?)"
                   ")"
                   ")) ";
            std::string pat = "%" + *query + "%";
            params.push_back(pat);
            params.push_back(pat);
            params.push_back(pat);
            params.push_back(pat);
        }

        if (sortBy == "name") {
            sql += "ORDER BY c.name ASC, c.jid ASC ";
        } else {
            sql += "ORDER BY c.last_message_time DESC, c.jid ASC ";
        }
        sql += "LIMIT ? OFFSET ?";
        params.push_back(static_cast<int64_t>(limit));
        params.push_back(static_cast<int64_t>(offset));

        std::lock_guard<std::mutex> lock(db_mutex_for(msgPath_));
        auto db = openDb();
        slimr::IterateOptions opts;
        opts.readOnlyConnection = (msgPath_ != ":memory:");
        opts.setupSql = build_setup_sql(waPath_);
        auto res = db->iterateLimited(sql, params, opts);

        std::vector<ChatRow> chats;
        for (const auto& row : res.rows) {
            ChatRow c;
            c.jid = json_val<std::string>(row, "jid");
            c.name = json_val<std::string>(row, "name");
            c.is_group = json_val<int>(row, "is_group", 0) != 0;
            if (row.contains("last_message_time") && row["last_message_time"].is_string()) {
                c.last_message_time = row["last_message_time"].get<std::string>();
            }
            if (row.contains("content") && row["content"].is_string()) {
                c.last_message = row["content"].get<std::string>();
            }
            if (row.contains("sender") && row["sender"].is_string()) {
                c.last_sender = row["sender"].get<std::string>();
            }
            if (row.contains("is_from_me") && !row["is_from_me"].is_null()) {
                c.last_is_from_me = json_val<bool>(row, "is_from_me");
            }
            chats.push_back(std::move(c));
        }
        return chats;
    }

    std::vector<ContactRow> searchContacts(const std::string& query) const {
        std::string pattern = "%" + query + "%";
        std::lock_guard<std::mutex> lock(db_mutex_for(msgPath_));
        auto db = openDb();
        slimr::IterateOptions opts;
        opts.readOnlyConnection = (msgPath_ != ":memory:");
        opts.setupSql = build_setup_sql(waPath_);

        // Query chats and t_contacts
        std::string sql = R"(
SELECT DISTINCT jid, name, phone FROM (
    SELECT jid, name, CASE WHEN jid LIKE '%@%' THEN substr(jid, 1, instr(jid, '@') - 1) ELSE jid END AS phone
    FROM chats
    WHERE (LOWER(name) LIKE LOWER(?) OR LOWER(jid) LIKE LOWER(?)) AND jid NOT LIKE '%@g.us'
    UNION
    SELECT their_jid AS jid, COALESCE(NULLIF(full_name, ''), NULLIF(push_name, ''), NULLIF(business_name, '')) AS name,
           CASE WHEN their_jid LIKE '%@%' THEN substr(their_jid, 1, instr(their_jid, '@') - 1) ELSE their_jid END AS phone
    FROM t_contacts
    WHERE (LOWER(full_name) LIKE LOWER(?) OR LOWER(push_name) LIKE LOWER(?) OR LOWER(their_jid) LIKE LOWER(?)) AND their_jid NOT LIKE '%@g.us'
) WHERE jid != '' ORDER BY name, jid LIMIT 50)";

        auto res = db->iterateLimited(sql, {pattern, pattern, pattern, pattern, pattern}, opts);
        std::vector<ContactRow> contacts;
        for (const auto& row : res.rows) {
            ContactRow cr;
            cr.jid = json_val<std::string>(row, "jid");
            cr.name = json_val<std::string>(row, "name");
            cr.phone_number = json_val<std::string>(row, "phone");
            contacts.push_back(std::move(cr));
        }
        return contacts;
    }

    ChatRow getChat(const std::string& chatJid, bool /*includeLast*/ = true) const {
        std::string sql = "SELECT c.jid, c.name, (c.jid LIKE '%@g.us') AS is_group, c.last_message_time, "
                          "m.content, m.sender, m.is_from_me FROM chats c "
                          "LEFT JOIN messages m ON c.jid = m.chat_jid AND c.last_message_time = m.timestamp "
                          "WHERE c.jid = ?";
        std::lock_guard<std::mutex> lock(db_mutex_for(msgPath_));
        auto db = openDb();
        slimr::IterateOptions opts;
        opts.readOnlyConnection = (msgPath_ != ":memory:");
        opts.setupSql = build_setup_sql(waPath_);
        auto res = db->iterateLimited(sql, {chatJid}, opts);
        if (res.rows.empty()) {
            throw std::runtime_error("chat " + chatJid + " not found");
        }
        const auto& row = res.rows.front();
        ChatRow c;
        c.jid = json_val<std::string>(row, "jid");
        c.name = json_val<std::string>(row, "name");
        c.is_group = json_val<int>(row, "is_group", 0) != 0;
        if (row.contains("last_message_time") && row["last_message_time"].is_string()) {
            c.last_message_time = row["last_message_time"].get<std::string>();
        }
        if (row.contains("content") && row["content"].is_string()) {
            c.last_message = row["content"].get<std::string>();
        }
        if (row.contains("sender") && row["sender"].is_string()) {
            c.last_sender = row["sender"].get<std::string>();
        }
        if (row.contains("is_from_me") && !row["is_from_me"].is_null()) {
            c.last_is_from_me = json_val<bool>(row, "is_from_me");
        }
        return c;
    }

    ChatRow getDirectChatByContact(const std::string& phone) const {
        std::string sql = "SELECT c.jid, c.name, (c.jid LIKE '%@g.us') AS is_group, c.last_message_time, "
                          "m.content, m.sender, m.is_from_me FROM chats c "
                          "LEFT JOIN messages m ON c.jid = m.chat_jid AND c.last_message_time = m.timestamp "
                          "WHERE c.jid LIKE ? AND c.jid NOT LIKE '%@g.us' LIMIT 1";
        std::lock_guard<std::mutex> lock(db_mutex_for(msgPath_));
        auto db = openDb();
        slimr::IterateOptions opts;
        opts.readOnlyConnection = (msgPath_ != ":memory:");
        opts.setupSql = build_setup_sql(waPath_);
        auto res = db->iterateLimited(sql, {"%" + phone + "%"}, opts);
        if (res.rows.empty()) {
            throw std::runtime_error("no direct chat found for " + phone);
        }
        const auto& row = res.rows.front();
        ChatRow c;
        c.jid = json_val<std::string>(row, "jid");
        c.name = json_val<std::string>(row, "name");
        c.is_group = false;
        if (row.contains("last_message_time") && row["last_message_time"].is_string()) {
            c.last_message_time = row["last_message_time"].get<std::string>();
        }
        if (row.contains("content") && row["content"].is_string()) {
            c.last_message = row["content"].get<std::string>();
        }
        if (row.contains("sender") && row["sender"].is_string()) {
            c.last_sender = row["sender"].get<std::string>();
        }
        if (row.contains("is_from_me") && !row["is_from_me"].is_null()) {
            c.last_is_from_me = json_val<bool>(row, "is_from_me");
        }
        return c;
    }

    std::vector<ChatRow> getContactChats(const std::string& jid, int limit = 20, int page = 0) const {
        if (limit <= 0) limit = 20;
        if (page < 0) page = 0;
        int offset = page * limit;

        std::string sql = "SELECT DISTINCT c.jid, c.name, (c.jid LIKE '%@g.us') AS is_group, c.last_message_time, "
                          "m.content, m.sender, m.is_from_me FROM chats c "
                          "JOIN messages m ON c.jid = m.chat_jid "
                          "WHERE m.sender = ? OR c.jid = ? "
                          "ORDER BY c.last_message_time DESC LIMIT ? OFFSET ?";
        std::lock_guard<std::mutex> lock(db_mutex_for(msgPath_));
        auto db = openDb();
        slimr::IterateOptions opts;
        opts.readOnlyConnection = (msgPath_ != ":memory:");
        opts.setupSql = build_setup_sql(waPath_);
        auto res = db->iterateLimited(sql, {jid, jid, static_cast<int64_t>(limit), static_cast<int64_t>(offset)}, opts);

        std::vector<ChatRow> chats;
        for (const auto& row : res.rows) {
            ChatRow c;
            c.jid = json_val<std::string>(row, "jid");
            c.name = json_val<std::string>(row, "name");
            c.is_group = json_val<int>(row, "is_group", 0) != 0;
            if (row.contains("last_message_time") && row["last_message_time"].is_string()) {
                c.last_message_time = row["last_message_time"].get<std::string>();
            }
            if (row.contains("content") && row["content"].is_string()) {
                c.last_message = row["content"].get<std::string>();
            }
            if (row.contains("sender") && row["sender"].is_string()) {
                c.last_sender = row["sender"].get<std::string>();
            }
            if (row.contains("is_from_me") && !row["is_from_me"].is_null()) {
                c.last_is_from_me = json_val<bool>(row, "is_from_me");
            }
            chats.push_back(std::move(c));
        }
        return chats;
    }

    std::string listMessages(const std::optional<std::string>& after = std::nullopt,
                             const std::optional<std::string>& before = std::nullopt,
                             const std::optional<std::string>& sender = std::nullopt,
                             const std::optional<std::string>& chatJid = std::nullopt,
                             const std::optional<std::string>& query = std::nullopt,
                             int limit = 200, int page = 0,
                             bool includeContext = true,
                             int ctxBefore = 1, int ctxAfter = 1) const {
        if (limit <= 0) limit = 200;
        if (page < 0) page = 0;

        std::lock_guard<std::mutex> lock(db_mutex_for(msgPath_));
        auto db = openDb();
        slimr::IterateOptions opts;
        opts.readOnlyConnection = (msgPath_ != ":memory:");
        opts.setupSql = build_setup_sql(waPath_);

        std::vector<MessageRow> msgs;

        if (query && !query->empty()) {
            std::string safeQ;
            for (char c : *query) {
                if (c == '"') safeQ += "\"\"";
                else safeQ += c;
            }
            std::string ftsMatch = "\"" + safeQ + "\"";

            std::string sql = "SELECT messages.timestamp, messages.sender, chats.name, messages.content, "
                              "messages.is_from_me, chats.jid, messages.id, messages.media_type "
                              "FROM messages_fts "
                              "JOIN messages ON messages_fts.rowid = messages.rowid "
                              "JOIN chats ON messages.chat_jid = chats.jid "
                              "WHERE messages_fts MATCH ? ";
            std::vector<slimr::SqlValue> params = {ftsMatch};

            if (chatJid && !chatJid->empty()) {
                sql += "AND messages.chat_jid = ? ";
                params.push_back(*chatJid);
            }
            if (after && !after->empty()) {
                sql += "AND messages.timestamp > ? ";
                params.push_back(*after);
            }
            if (before && !before->empty()) {
                sql += "AND messages.timestamp < ? ";
                params.push_back(*before);
            }
            sql += "ORDER BY bm25(messages_fts) LIMIT ?";
            params.push_back(static_cast<int64_t>(limit));

            auto res = db->iterateLimited(sql, params, opts);
            for (const auto& row : res.rows) {
                MessageRow m;
                m.timestamp = json_val<std::string>(row, "timestamp");
                m.sender = json_val<std::string>(row, "sender");
                m.chat_name = json_val<std::string>(row, "name");
                m.content = json_val<std::string>(row, "content");
                m.is_from_me = json_val<int>(row, "is_from_me", 0) != 0;
                m.chat_jid = json_val<std::string>(row, "jid");
                m.id = json_val<std::string>(row, "id");
                m.media_type = json_val<std::string>(row, "media_type");
                if (sender && !sender->empty() && m.sender != *sender) continue;
                msgs.push_back(std::move(m));
            }
        } else {
            std::string sql = "SELECT messages.timestamp, messages.sender, chats.name, messages.content, "
                              "messages.is_from_me, chats.jid, messages.id, messages.media_type "
                              "FROM messages JOIN chats ON messages.chat_jid = chats.jid ";
            std::vector<std::string> where;
            std::vector<slimr::SqlValue> params;

            if (after && !after->empty()) {
                where.push_back("messages.timestamp > ?");
                params.push_back(*after);
            }
            if (before && !before->empty()) {
                where.push_back("messages.timestamp < ?");
                params.push_back(*before);
            }
            if (sender && !sender->empty()) {
                where.push_back("messages.sender = ?");
                params.push_back(*sender);
            }
            if (chatJid && !chatJid->empty()) {
                where.push_back("messages.chat_jid = ?");
                params.push_back(*chatJid);
            }
            if (!where.empty()) {
                sql += "WHERE ";
                for (size_t i = 0; i < where.size(); ++i) {
                    if (i > 0) sql += " AND ";
                    sql += where[i];
                }
                sql += " ";
            }
            sql += "ORDER BY messages.timestamp DESC LIMIT ? OFFSET ?";
            params.push_back(static_cast<int64_t>(limit));
            params.push_back(static_cast<int64_t>(page * limit));

            auto res = db->iterateLimited(sql, params, opts);
            for (const auto& row : res.rows) {
                MessageRow m;
                m.timestamp = json_val<std::string>(row, "timestamp");
                m.sender = json_val<std::string>(row, "sender");
                m.chat_name = json_val<std::string>(row, "name");
                m.content = json_val<std::string>(row, "content");
                m.is_from_me = json_val<int>(row, "is_from_me", 0) != 0;
                m.chat_jid = json_val<std::string>(row, "jid");
                m.id = json_val<std::string>(row, "id");
                m.media_type = json_val<std::string>(row, "media_type");
                msgs.push_back(std::move(m));
            }
        }

        if (includeContext && !msgs.empty() && (ctxBefore > 0 || ctxAfter > 0)) {
            msgs = expandContext(db.get(), msgs, ctxBefore, ctxAfter);
        }

        if (msgs.empty()) {
            return std::string(UNTRUSTED_HEADER) + "No messages to display.";
        }

        std::string text = std::string(UNTRUSTED_HEADER);
        for (const auto& m : msgs) {
            text += format_message(m, resolveSenderName(db.get(), m.sender));
        }
        return text;
    }

    std::string resolveSenderName(slimr::SqliteDb* db, const std::string& sender) const {
        if (sender.empty()) return "";
        try {
            std::string phonePart = sender;
            auto atPos = sender.find('@');
            if (atPos != std::string::npos) phonePart = sender.substr(0, atPos);

            slimr::IterateOptions opts;
            opts.readOnlyConnection = (msgPath_ != ":memory:");
            opts.setupSql = build_setup_sql(waPath_);
            auto res = db->iterateLimited("SELECT name FROM t_names WHERE user = ? OR user = ? LIMIT 1", std::vector<slimr::SqlValue>{sender, phonePart}, opts);
            if (!res.rows.empty()) {
                std::string n = json_val<std::string>(res.rows.front(), "name");
                if (!n.empty()) return n;
            }
        } catch (...) {}
        return sender;
    }

    std::vector<MessageRow> expandContext(slimr::SqliteDb* db, const std::vector<MessageRow>& msgs, int before, int after) const {
        if (msgs.empty() || (before <= 0 && after <= 0)) return msgs;

        std::vector<MessageRow> expanded;
        for (const auto& msg : msgs) {
            // Fetch before
            std::string sqlBefore = "SELECT messages.timestamp, messages.sender, chats.name, messages.content, "
                                   "messages.is_from_me, chats.jid, messages.id, messages.media_type "
                                   "FROM messages JOIN chats ON messages.chat_jid = chats.jid "
                                   "WHERE messages.chat_jid = ? AND messages.timestamp < ? "
                                   "ORDER BY messages.timestamp DESC LIMIT ?";
            slimr::IterateOptions opts;
            opts.readOnlyConnection = (msgPath_ != ":memory:");
            opts.setupSql = build_setup_sql(waPath_);
            auto resBefore = db->iterateLimited(sqlBefore, {msg.chat_jid, msg.timestamp, static_cast<int64_t>(before)}, opts);
            std::vector<MessageRow> bList;
            for (const auto& row : resBefore.rows) {
                MessageRow m;
                m.timestamp = json_val<std::string>(row, "timestamp");
                m.sender = json_val<std::string>(row, "sender");
                m.chat_name = json_val<std::string>(row, "name");
                m.content = json_val<std::string>(row, "content");
                m.is_from_me = json_val<int>(row, "is_from_me", 0) != 0;
                m.chat_jid = json_val<std::string>(row, "jid");
                m.id = json_val<std::string>(row, "id");
                m.media_type = json_val<std::string>(row, "media_type");
                bList.push_back(std::move(m));
            }
            std::reverse(bList.begin(), bList.end());

            // Fetch after
            std::string sqlAfter = "SELECT messages.timestamp, messages.sender, chats.name, messages.content, "
                                  "messages.is_from_me, chats.jid, messages.id, messages.media_type "
                                  "FROM messages JOIN chats ON messages.chat_jid = chats.jid "
                                  "WHERE messages.chat_jid = ? AND messages.timestamp > ? "
                                  "ORDER BY messages.timestamp ASC LIMIT ?";
            auto resAfter = db->iterateLimited(sqlAfter, {msg.chat_jid, msg.timestamp, static_cast<int64_t>(after)}, opts);
            std::vector<MessageRow> aList;
            for (const auto& row : resAfter.rows) {
                MessageRow m;
                m.timestamp = json_val<std::string>(row, "timestamp");
                m.sender = json_val<std::string>(row, "sender");
                m.chat_name = json_val<std::string>(row, "name");
                m.content = json_val<std::string>(row, "content");
                m.is_from_me = json_val<int>(row, "is_from_me", 0) != 0;
                m.chat_jid = json_val<std::string>(row, "jid");
                m.id = json_val<std::string>(row, "id");
                m.media_type = json_val<std::string>(row, "media_type");
                aList.push_back(std::move(m));
            }

            expanded.insert(expanded.end(), bList.begin(), bList.end());
            expanded.push_back(msg);
            expanded.insert(expanded.end(), aList.begin(), aList.end());
        }
        return expanded;
    }

    slimr::Json getMessageContext(const std::string& messageId, int before = 5, int after = 5) const {
        std::lock_guard<std::mutex> lock(db_mutex_for(msgPath_));
        auto db = openDb();
        slimr::IterateOptions opts;
        opts.readOnlyConnection = (msgPath_ != ":memory:");
        opts.setupSql = build_setup_sql(waPath_);

        std::string sqlTarget = "SELECT messages.timestamp, messages.sender, chats.name, messages.content, "
                               "messages.is_from_me, chats.jid, messages.id, messages.media_type "
                               "FROM messages JOIN chats ON messages.chat_jid = chats.jid "
                               "WHERE messages.id = ?";
        auto resTarget = db->iterateLimited(sqlTarget, {messageId}, opts);
        if (resTarget.rows.empty()) {
            throw std::runtime_error("message " + messageId + " not found");
        }
        const auto& tRow = resTarget.rows.front();
        MessageRow target;
        target.timestamp = json_val<std::string>(tRow, "timestamp");
        target.sender = json_val<std::string>(tRow, "sender");
        target.chat_name = json_val<std::string>(tRow, "name");
        target.content = json_val<std::string>(tRow, "content");
        target.is_from_me = json_val<int>(tRow, "is_from_me", 0) != 0;
        target.chat_jid = json_val<std::string>(tRow, "jid");
        target.id = json_val<std::string>(tRow, "id");
        target.media_type = json_val<std::string>(tRow, "media_type");

        std::string sqlBefore = "SELECT messages.timestamp, messages.sender, chats.name, messages.content, "
                               "messages.is_from_me, chats.jid, messages.id, messages.media_type "
                               "FROM messages JOIN chats ON messages.chat_jid = chats.jid "
                               "WHERE messages.chat_jid = ? AND messages.timestamp < ? "
                               "ORDER BY messages.timestamp DESC LIMIT ?";
        auto resBefore = db->iterateLimited(sqlBefore, {target.chat_jid, target.timestamp, static_cast<int64_t>(before)}, opts);
        slimr::Json beforeArr = slimr::Json::array_t{};
        std::vector<slimr::Json> bList;
        for (const auto& r : resBefore.rows) {
            slimr::Json m = slimr::Json::object_t{};
            m["timestamp"] = json_val<std::string>(r, "timestamp");
            m["sender"] = json_val<std::string>(r, "sender");
            m["chat_name"] = json_val<std::string>(r, "name");
            m["content"] = json_val<std::string>(r, "content");
            m["is_from_me"] = json_val<int>(r, "is_from_me", 0) != 0;
            m["chat_jid"] = json_val<std::string>(r, "jid");
            m["id"] = json_val<std::string>(r, "id");
            if (r.contains("media_type") && !r["media_type"].is_null() && !json_val<std::string>(r, "media_type").empty()) {
                m["media_type"] = json_val<std::string>(r, "media_type");
            }
            bList.push_back(std::move(m));
        }
        std::reverse(bList.begin(), bList.end());
        for (auto& item : bList) beforeArr.get_array().push_back(std::move(item));

        std::string sqlAfter = "SELECT messages.timestamp, messages.sender, chats.name, messages.content, "
                              "messages.is_from_me, chats.jid, messages.id, messages.media_type "
                              "FROM messages JOIN chats ON messages.chat_jid = chats.jid "
                              "WHERE messages.chat_jid = ? AND messages.timestamp > ? "
                              "ORDER BY messages.timestamp ASC LIMIT ?";
        auto resAfter = db->iterateLimited(sqlAfter, {target.chat_jid, target.timestamp, static_cast<int64_t>(after)}, opts);
        slimr::Json afterArr = slimr::Json::array_t{};
        for (const auto& r : resAfter.rows) {
            slimr::Json m = slimr::Json::object_t{};
            m["timestamp"] = json_val<std::string>(r, "timestamp");
            m["sender"] = json_val<std::string>(r, "sender");
            m["chat_name"] = json_val<std::string>(r, "name");
            m["content"] = json_val<std::string>(r, "content");
            m["is_from_me"] = json_val<int>(r, "is_from_me", 0) != 0;
            m["chat_jid"] = json_val<std::string>(r, "jid");
            m["id"] = json_val<std::string>(r, "id");
            if (r.contains("media_type") && !r["media_type"].is_null() && !json_val<std::string>(r, "media_type").empty()) {
                m["media_type"] = json_val<std::string>(r, "media_type");
            }
            afterArr.get_array().push_back(std::move(m));
        }

        slimr::Json targetJson = slimr::Json::object_t{};
        targetJson["timestamp"] = target.timestamp;
        targetJson["sender"] = target.sender;
        targetJson["chat_name"] = target.chat_name;
        targetJson["content"] = target.content;
        targetJson["is_from_me"] = target.is_from_me;
        targetJson["chat_jid"] = target.chat_jid;
        targetJson["id"] = target.id;
        if (!target.media_type.empty()) targetJson["media_type"] = target.media_type;

        slimr::Json res = slimr::Json::object_t{};
        res["message"] = targetJson;
        res["before"] = beforeArr;
        res["after"] = afterArr;
        return res;
    }

    std::string getLastInteraction(const std::string& jid) const {
        std::lock_guard<std::mutex> lock(db_mutex_for(msgPath_));
        auto db = openDb();
        slimr::IterateOptions opts;
        opts.readOnlyConnection = (msgPath_ != ":memory:");
        opts.setupSql = build_setup_sql(waPath_);

        std::string sql = "SELECT messages.timestamp, messages.sender, chats.name, messages.content, "
                          "messages.is_from_me, chats.jid, messages.id, messages.media_type "
                          "FROM messages JOIN chats ON messages.chat_jid = chats.jid "
                          "WHERE messages.sender = ? OR chats.jid = ? "
                          "ORDER BY messages.timestamp DESC LIMIT 1";
        auto res = db->iterateLimited(sql, std::vector<slimr::SqlValue>{jid, jid}, opts);
        if (res.rows.empty()) {
            throw std::runtime_error("no interaction found for " + jid);
        }
        const auto& r = res.rows.front();
        MessageRow m;
        m.timestamp = json_val<std::string>(r, "timestamp");
        m.sender = json_val<std::string>(r, "sender");
        m.chat_name = json_val<std::string>(r, "name");
        m.content = json_val<std::string>(r, "content");
        m.is_from_me = json_val<int>(r, "is_from_me", 0) != 0;
        m.chat_jid = json_val<std::string>(r, "jid");
        m.id = json_val<std::string>(r, "id");
        m.media_type = json_val<std::string>(r, "media_type");

        return std::string(UNTRUSTED_HEADER) + format_message(m, resolveSenderName(db.get(), m.sender));
    }

private:
    std::string dataDir_;
    std::string msgPath_;
    std::string waPath_;
};

} // namespace whatsapp
