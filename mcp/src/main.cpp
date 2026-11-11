#include "app.hpp"
#include "oauth_as.hpp"

#include <mcp_http.hpp>

#include <cstdlib>
#include <string>
#include <string_view>

int main(int argc, char** argv) {
    auto app = whatsapp::make_whatsapp_app();

    std::string host = "127.0.0.1";
    int port = 8088;
    for (int i = 2; i < argc; ++i) {
        std::string_view arg = argv[i];
        if (arg == "--host" && i + 1 < argc) {
            host = argv[++i];
        } else if (arg == "--port" && i + 1 < argc) {
            char* endPtr = nullptr;
            long parsed = std::strtol(argv[++i], &endPtr, 10);
            if (endPtr != nullptr && *endPtr == '\0' && parsed >= 0 && parsed <= 65535) {
                port = static_cast<int>(parsed);
            }
        }
    }

    slimr::HttpServerOptions httpOpts;
    httpOpts.host = "127.0.0.1";
    httpOpts.port = 8088;
    httpOpts.allowEphemeralPort = false;
    httpOpts.allowNonLoopback = false;

    const std::string dataDir = whatsapp::resolve_data_dir();
    const std::string sockPath = whatsapp::resolve_socket_path(dataDir);
    const std::string baseUrl = "http://" + host + ":" + std::to_string(port > 0 ? port : 8088);

    auto oauthAs = whatsapp::configure_whatsapp_http_oauth(
        httpOpts,
        whatsapp::OAuthAsConfig{
            .daemonSocketPath = sockPath,
            .defaultBaseUrl = baseUrl,
            .mcpPath = httpOpts.mcpPath,
        },
        port > 0);

    slimr::enable_http(app, std::move(httpOpts));
    return app.run(argc, argv);
}
