#include "app.hpp"
#include "log_file.hpp"
#include "oauth_as.hpp"

#include <mcp_http.hpp>

#include <cstdlib>
#include <iostream>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

int main(int argc, char **argv) {
    std::optional<std::string> logFile;
    std::vector<char *> filteredArgv;
    if (argc > 0 && argv != nullptr) {
        filteredArgv.reserve(static_cast<size_t>(argc));
        filteredArgv.push_back(argv[0]);
        for (int i = 1; i < argc; ++i) {
            const char *arg = argv[i];
            if (arg == nullptr)
                continue;
            std::string_view sv(arg);
            if (!sv.empty() && sv.front() == '{') {
                filteredArgv.push_back(argv[i]);
                continue;
            }
            if (sv == "--log-file") {
                if (i + 1 >= argc || argv[i + 1] == nullptr || argv[i + 1][0] == '\0') {
                    std::cerr << "Missing path for --log-file\n";
                    return 1;
                }
                logFile = argv[++i];
            } else if (sv.starts_with("--log-file=")) {
                std::string_view val = sv.substr(std::string_view("--log-file=").size());
                if (val.empty()) {
                    std::cerr << "Missing path for --log-file\n";
                    return 1;
                }
                logFile = std::string(val);
            } else {
                filteredArgv.push_back(argv[i]);
            }
        }
    }

    std::unique_ptr<app_log::LogFileGuard> logGuard;
    if (logFile.has_value()) {
        auto opened = app_log::LogFileGuard::open(*logFile);
        if (!opened) {
            std::cerr << opened.error() << "\n";
            return 1;
        }
        logGuard = std::move(*opened);
    }

    auto app = whatsapp::make_whatsapp_app();

    std::string host = "127.0.0.1";
    int port = 10008;
    for (size_t i = 2; i < filteredArgv.size(); ++i) {
        std::string_view arg = filteredArgv[i];
        if (arg == "--host" && i + 1 < filteredArgv.size()) {
            host = filteredArgv[++i];
        } else if (arg == "--port" && i + 1 < filteredArgv.size()) {
            char *endPtr = nullptr;
            long parsed = std::strtol(filteredArgv[++i], &endPtr, 10);
            if (endPtr != nullptr && *endPtr == '\0' && parsed >= 0 && parsed <= 65535) {
                port = static_cast<int>(parsed);
            }
        }
    }

    slimr::McpHttpOptions httpOpts;
    httpOpts.host = host;
    httpOpts.port = static_cast<uint16_t>(port);

    const std::string dataDir = whatsapp::resolve_data_dir();
    const std::string sockPath = whatsapp::resolve_socket_path(dataDir);
    // OAuth metadata advertises loopback when bound to all interfaces
    const std::string publicHost = (host.empty() || host == "0.0.0.0") ? "127.0.0.1" : host;
    const std::string baseUrl = "http://" + publicHost + ":" + std::to_string(port > 0 ? port : 10008);
    if (port > 0) {
        httpOpts.publicBaseUrl = baseUrl;
    }

    auto oauthAs = whatsapp::configure_whatsapp_http_oauth(
        httpOpts,
        whatsapp::OAuthAsConfig{
            .daemonSocketPath = sockPath,
            .defaultBaseUrl = baseUrl,
            .mcpPath = httpOpts.mcpPath,
        },
        port > 0);

    slimr::enable_http(app, std::move(httpOpts));
    return app.run(static_cast<int>(filteredArgv.size()), filteredArgv.data());
}
