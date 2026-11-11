#pragma once

#include "curl.hpp"
#include "json.hpp"

#include <cstdlib>
#include <filesystem>
#include <stdexcept>
#include <string>

namespace whatsapp {

inline std::string resolve_data_dir() {
    const char* clientEnv = std::getenv("WHATSAPP_CLIENT_DATA_DIR");
    if (clientEnv && clientEnv[0] != '\0') return clientEnv;

    const char* mcpEnv = std::getenv("WHATSAPP_MCP_DATA_DIR");
    if (mcpEnv && mcpEnv[0] != '\0') return mcpEnv;

    const char* home = std::getenv("HOME");
    std::string homeDir = home ? home : ".";
    std::string clientDir = homeDir + "/.local/share/whatsapp-client";
    std::string mcpDir = homeDir + "/.local/share/whatsapp-mcp";

    if (std::filesystem::exists(clientDir)) {
        return clientDir;
    }
    if (std::filesystem::exists(mcpDir)) {
        return mcpDir;
    }
    return clientDir;
}

inline std::string resolve_socket_path(const std::string& dataDir) {
    std::string sock = dataDir + "/daemon.sock";
    if (std::filesystem::exists(sock)) {
        return sock;
    }

    const char* home = std::getenv("HOME");
    std::string homeDir = home ? home : ".";
    std::string clientSock = homeDir + "/.local/share/whatsapp-client/daemon.sock";
    if (std::filesystem::exists(clientSock)) {
        return clientSock;
    }
    std::string mcpSock = homeDir + "/.local/share/whatsapp-mcp/daemon.sock";
    if (std::filesystem::exists(mcpSock)) {
        return mcpSock;
    }
    return sock;
}

struct SendResult {
    bool success = false;
    std::string message;
};

struct DownloadedMedia {
    std::string path;
    std::string mediaType;
    std::string filename;
};

inline SendResult daemon_send(const std::string& sockPath, const std::string& recipient, const std::string& message, const std::string& mediaPath = "") {
    slimr::HttpRequest req;
    req.method = "POST";
    req.url = "http://localhost/api/send";
    req.unixSocketPath = sockPath;
    req.headers["Content-Type"] = "application/json";

    slimr::Json body = slimr::Json::object_t{};
    body["recipient"] = recipient;
    if (!message.empty()) body["message"] = message;
    if (!mediaPath.empty()) body["media_path"] = mediaPath;
    req.body = slimr::write_json(body);

    auto resp = slimr::request(req);
    if (resp.status != 200 && resp.body.empty()) {
        throw std::runtime_error("daemon request failed with HTTP " + std::to_string(resp.status));
    }

    auto jRes = slimr::parse_json(resp.body);
    SendResult r;
    if (jRes) {
        const auto& j = *jRes;
        if (j.is_object() && j.contains("success")) {
            r.success = j["success"].is_boolean() ? j["success"].get<bool>() : false;
            if (j.contains("message") && j["message"].is_string()) {
                r.message = j["message"].get<std::string>();
            }
        } else {
            r.message = resp.body;
        }
    } else {
        r.message = resp.body;
    }
    return r;
}

inline DownloadedMedia daemon_download(const std::string& sockPath, const std::string& messageId, const std::string& chatJid, const std::string& dataDir) {
    slimr::HttpRequest req;
    req.method = "POST";
    req.url = "http://localhost/api/download";
    req.unixSocketPath = sockPath;
    req.headers["Content-Type"] = "application/json";

    slimr::Json body = slimr::Json::object_t{};
    body["message_id"] = messageId;
    body["chat_jid"] = chatJid;
    req.body = slimr::write_json(body);

    auto resp = slimr::request(req);
    if (resp.status != 200 && resp.body.empty()) {
        throw std::runtime_error("daemon request failed with HTTP " + std::to_string(resp.status));
    }

    auto jRes = slimr::parse_json(resp.body);
    if (!jRes) {
        throw std::runtime_error("failed to parse daemon download response JSON: " + jRes.error());
    }
    const auto& j = *jRes;
    if (!j.is_object() || !j.contains("success") || !j["success"].is_boolean() || !j["success"].get<bool>()) {
        std::string err = "download failed";
        if (j.is_object() && j.contains("message") && j["message"].is_string()) {
            err = j["message"].get<std::string>();
        }
        throw std::runtime_error(err);
    }

    DownloadedMedia m;
    m.path = j.contains("path") && j["path"].is_string() ? j["path"].get<std::string>() : "";
    m.mediaType = j.contains("media_type") && j["media_type"].is_string() ? j["media_type"].get<std::string>() : "";
    m.filename = j.contains("filename") && j["filename"].is_string() ? j["filename"].get<std::string>() : "";

    // Verify downloaded path is inside downloads/
    std::filesystem::path dlDir = std::filesystem::path(dataDir) / "downloads";
    std::error_code ec;
    auto canPath = std::filesystem::canonical(m.path, ec);
    if (!ec) {
        auto canDl = std::filesystem::canonical(dlDir, ec);
        if (!ec) {
            auto rel = std::filesystem::relative(canPath, canDl, ec);
            if (ec || rel.string().starts_with("..")) {
                throw std::runtime_error("downloaded media path is outside downloads directory: " + m.path);
            }
        }
    }

    return m;
}

} // namespace whatsapp
