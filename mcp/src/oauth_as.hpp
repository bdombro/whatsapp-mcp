#pragma once

#include "daemon_client.hpp"
#include "qr_svg.hpp"

#include "json.hpp"
#include "mcp_http.hpp"
#include "mcp_oauth.hpp"

#include <openssl/crypto.h>
#include <openssl/evp.h>
#include <openssl/rand.h>

#include <algorithm>
#include <array>
#include <chrono>
#include <cstdint>
#include <deque>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <sstream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace whatsapp {

namespace oauth_detail {

inline std::string b64url_encode(const uint8_t* data, size_t len) {
    static constexpr char kTbl[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
    std::string out;
    out.reserve(((len + 2) / 3) * 4);
    size_t val = 0;
    int valb = -6;
    for (size_t i = 0; i < len; ++i) {
        val = (val << 8) | data[i];
        valb += 8;
        while (valb >= 0) {
            out.push_back(kTbl[(val >> valb) & 0x3F]);
            valb -= 6;
        }
    }
    if (valb > -6) {
        out.push_back(kTbl[((val << 8) >> (valb + 8)) & 0x3F]);
    }
    return out;
}

inline std::string b64url_encode_str(std::string_view s) {
    return b64url_encode(reinterpret_cast<const uint8_t*>(s.data()), s.size());
}

inline std::string random_token(size_t numBytes = 32) {
    if (numBytes == 0 || numBytes > 1024) {
        throw std::invalid_argument("invalid random token byte count");
    }
    std::vector<uint8_t> buf(numBytes);
    if (RAND_bytes(buf.data(), static_cast<int>(numBytes)) != 1) {
        throw std::runtime_error("CSPRNG failure generating token");
    }
    return b64url_encode(buf.data(), buf.size());
}

inline std::array<uint8_t, 32> sha256_digest(std::string_view input) {
    std::array<uint8_t, 32> digest{};
    unsigned int outLen = 0;
    if (EVP_Digest(input.data(), input.size(), digest.data(), &outLen, EVP_sha256(), nullptr) != 1 ||
        outLen != digest.size()) {
        throw std::runtime_error("SHA-256 digest calculation failed");
    }
    return digest;
}

inline std::string sha256_b64url(std::string_view input) {
    auto d = sha256_digest(input);
    return b64url_encode(d.data(), d.size());
}

inline bool constant_time_eq(std::string_view a, std::string_view b) {
    // Compare fixed-size SHA-256 digests via CRYPTO_memcmp so neither length nor prefix leaks.
    auto da = sha256_digest(a);
    auto db = sha256_digest(b);
    return CRYPTO_memcmp(da.data(), db.data(), da.size()) == 0;
}

inline bool is_valid_pkce_verifier(std::string_view v) noexcept {
    if (v.size() < 43 || v.size() > 128) return false;
    for (unsigned char c : v) {
        bool ok = (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
                  c == '-' || c == '.' || c == '_' || c == '~';
        if (!ok) return false;
    }
    return true;
}

inline bool is_valid_b64url_challenge(std::string_view c) noexcept {
    if (c.size() != 43) return false;
    for (unsigned char ch : c) {
        bool ok = (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') ||
                  ch == '-' || ch == '_';
        if (!ok) return false;
    }
    return true;
}

inline std::string ascii_lower(std::string_view s) {
    std::string out(s);
    for (char& ch : out) {
        ch = static_cast<char>(std::tolower(static_cast<unsigned char>(ch)));
    }
    return out;
}

inline bool has_control_chars(std::string_view s) noexcept {
    for (unsigned char c : s) {
        if (c < 0x20 || c == 0x7F) return true;
    }
    return false;
}

inline std::string html_escape(std::string_view s) {
    std::string out;
    out.reserve(s.size());
    for (char c : s) {
        switch (c) {
            case '&': out += "&amp;"; break;
            case '<': out += "&lt;"; break;
            case '>': out += "&gt;"; break;
            case '"': out += "&quot;"; break;
            case '\'': out += "&#39;"; break;
            default: out.push_back(c); break;
        }
    }
    return out;
}

inline std::string url_encode(std::string_view s) {
    static constexpr char kHex[] = "0123456789ABCDEF";
    std::string out;
    out.reserve(s.size());
    for (unsigned char c : s) {
        bool unreserved = (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
                          (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~';
        if (unreserved) {
            out.push_back(static_cast<char>(c));
        } else {
            out.push_back('%');
            out.push_back(kHex[(c >> 4) & 0x0F]);
            out.push_back(kHex[c & 0x0F]);
        }
    }
    return out;
}

inline std::string url_decode(std::string_view s) {
    auto hexVal = [](char c) -> int {
        if (c >= '0' && c <= '9') return c - '0';
        if (c >= 'a' && c <= 'f') return c - 'a' + 10;
        if (c >= 'A' && c <= 'F') return c - 'A' + 10;
        return -1;
    };
    std::string res;
    res.reserve(s.size());
    for (size_t i = 0; i < s.size(); ++i) {
        if (s[i] == '+') {
            res.push_back(' ');
        } else if (s[i] == '%' && i + 2 < s.size()) {
            int hi = hexVal(s[i + 1]);
            int lo = hexVal(s[i + 2]);
            if (hi >= 0 && lo >= 0) {
                res.push_back(static_cast<char>((hi << 4) | lo));
                i += 2;
                continue;
            }
            res.push_back(s[i]);
        } else {
            res.push_back(s[i]);
        }
    }
    return res;
}

inline std::map<std::string, std::string> parse_form_urlencoded(std::string_view body) {
    std::map<std::string, std::string> out;
    size_t pos = 0;
    while (pos <= body.size()) {
        auto amp = body.find('&', pos);
        std::string_view pair = (amp == std::string_view::npos) ? body.substr(pos) : body.substr(pos, amp - pos);
        if (!pair.empty()) {
            auto eq = pair.find('=');
            if (eq == std::string_view::npos) {
                out[url_decode(pair)] = "";
            } else {
                out[url_decode(pair.substr(0, eq))] = url_decode(pair.substr(eq + 1));
            }
        }
        if (amp == std::string_view::npos) break;
        pos = amp + 1;
    }
    return out;
}

inline std::optional<std::string> extract_cookie_value(std::string_view cookieHeader, std::string_view name) {
    size_t pos = 0;
    while (pos < cookieHeader.size()) {
        auto semi = cookieHeader.find(';', pos);
        std::string_view item = (semi == std::string_view::npos)
            ? cookieHeader.substr(pos)
            : cookieHeader.substr(pos, semi - pos);
        while (!item.empty() && (item.front() == ' ' || item.front() == '\t')) item.remove_prefix(1);
        while (!item.empty() && (item.back() == ' ' || item.back() == '\t')) item.remove_suffix(1);
        auto eq = item.find('=');
        if (eq != std::string_view::npos && item.substr(0, eq) == name) {
            return std::string(item.substr(eq + 1));
        }
        if (semi == std::string_view::npos) break;
        pos = semi + 1;
    }
    return std::nullopt;
}

inline bool is_valid_loopback_redirect_uri(std::string_view uri) noexcept {
    if (uri.empty() || uri.size() > 2048 || has_control_chars(uri)) return false;
    if (uri.find('#') != std::string_view::npos || uri.find('@') != std::string_view::npos) return false;
    for (unsigned char c : uri) {
        if (c == ' ' || c == '\t') return false;
    }

    auto schemeEnd = uri.find("://");
    if (schemeEnd == std::string_view::npos) return false;
    if (ascii_lower(uri.substr(0, schemeEnd)) != "http") return false;

    std::string_view rest = uri.substr(schemeEnd + 3);
    auto pathPos = rest.find_first_of("/?");
    std::string_view authority = (pathPos == std::string_view::npos) ? rest : rest.substr(0, pathPos);
    if (authority.empty()) return false;

    std::string_view host;
    std::string_view portStr;
    if (authority.front() == '[') {
        auto closeBracket = authority.find(']');
        if (closeBracket == std::string_view::npos) return false;
        host = authority.substr(0, closeBracket + 1);
        std::string_view after = authority.substr(closeBracket + 1);
        if (!after.empty()) {
            if (after.front() != ':') return false;
            portStr = after.substr(1);
            if (portStr.empty()) return false;
        }
    } else {
        auto colon = authority.find(':');
        if (colon == std::string_view::npos) {
            host = authority;
        } else {
            host = authority.substr(0, colon);
            portStr = authority.substr(colon + 1);
            if (portStr.empty()) return false;
        }
    }

    std::string lowerHost = ascii_lower(host);
    if (lowerHost != "127.0.0.1" && lowerHost != "localhost" && lowerHost != "[::1]") {
        return false;
    }
    if (!portStr.empty()) {
        if (portStr.size() > 5) return false;
        unsigned long portVal = 0;
        for (char c : portStr) {
            if (c < '0' || c > '9') return false;
            portVal = portVal * 10 + static_cast<unsigned long>(c - '0');
        }
        if (portVal < 1 || portVal > 65535) return false;
    }
    return true;
}

inline std::string append_redirect_params(std::string_view baseUri,
                                          std::initializer_list<std::pair<std::string_view, std::string_view>> params) {
    std::string out(baseUri);
    bool hasQuery = (baseUri.find('?') != std::string_view::npos);
    for (const auto& [k, v] : params) {
        if (v.empty()) continue;
        out.push_back(hasQuery ? '&' : '?');
        hasQuery = true;
        out += url_encode(k);
        out.push_back('=');
        out += url_encode(v);
    }
    return out;
}

inline std::vector<std::string> parse_scopes(std::string_view raw) {
    std::vector<std::string> out;
    size_t i = 0;
    while (i < raw.size()) {
        while (i < raw.size() && raw[i] == ' ') ++i;
        if (i >= raw.size()) break;
        size_t j = i;
        while (j < raw.size() && raw[j] != ' ') ++j;
        std::string tok(raw.substr(i, j - i));
        if (std::find(out.begin(), out.end(), tok) == out.end()) {
            out.push_back(std::move(tok));
        }
        i = j;
    }
    return out;
}

inline bool is_supported_scope(std::string_view s) noexcept {
    return s == "whatsapp:read" || s == "whatsapp:write";
}

inline std::string join_scopes(const std::vector<std::string>& scopes) {
    std::string out;
    for (size_t i = 0; i < scopes.size(); ++i) {
        if (i > 0) out.push_back(' ');
        out += scopes[i];
    }
    return out;
}

inline std::string derive_base_url_from_request(const slimr::HttpRouteRequest& req, std::string_view fallback) {
    auto hostHdr = req.header("host");
    if (hostHdr.has_value() && !hostHdr->empty() && !has_control_chars(*hostHdr)) {
        bool validChars = true;
        for (unsigned char c : *hostHdr) {
            bool ok = (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
                      c == '.' || c == ':' || c == '[' || c == ']' || c == '-';
            if (!ok) {
                validChars = false;
                break;
            }
        }
        if (validChars) {
            return "http://" + *hostHdr;
        }
    }
    return std::string(fallback);
}

inline std::string derive_base_url_from_resource(std::string_view resourceUrl,
                                                 std::string_view mcpPath,
                                                 std::string_view fallback) {
    if (!mcpPath.empty() && resourceUrl.ends_with(mcpPath) && resourceUrl.size() > mcpPath.size()) {
        return std::string(resourceUrl.substr(0, resourceUrl.size() - mcpPath.size()));
    }
    return std::string(fallback);
}

inline void add_security_headers(slimr::HttpRouteResponse& resp, std::string_view csp) {
    resp.headers["Cache-Control"] = "no-store";
    resp.headers["Pragma"] = "no-cache";
    resp.headers["X-Frame-Options"] = "DENY";
    resp.headers["X-Content-Type-Options"] = "nosniff";
    resp.headers["Referrer-Policy"] = "no-referrer";
    if (!csp.empty()) {
        resp.headers["Content-Security-Policy"] = std::string(csp);
    }
}

} // namespace oauth_detail

struct OAuthAsConfig {
    std::string daemonSocketPath;
    std::string defaultBaseUrl{"http://127.0.0.1:8088"};
    std::string mcpPath{"/mcp"};
    int authTxTtlSeconds{300};
    int authCodeTtlSeconds{120};
    int accessTokenTtlSeconds{3600};
    int refreshTokenTtlSeconds{30 * 24 * 3600};
    int clientTtlSeconds{24 * 3600};
};

class OAuthAsServer {
public:
    explicit OAuthAsServer(OAuthAsConfig cfg) : cfg_(std::move(cfg)) {}

    slimr::TokenValidationResult validateBearerToken(const slimr::TokenValidationContext& tctx) {
        using Status = slimr::TokenValidationResult::Status;
        slimr::TokenValidationResult res;
        res.status = Status::InvalidToken;

        std::array<uint8_t, 32> tokenDigest{};
        try {
            tokenDigest = oauth_detail::sha256_digest(tctx.token);
        } catch (...) {
            res.errorDescription = "Token digest check failed";
            return res;
        }

        const auto now = std::chrono::system_clock::now();
        std::string expectedIssuer = oauth_detail::derive_base_url_from_resource(
            tctx.expectedResource, cfg_.mcpPath, cfg_.defaultBaseUrl);

        std::lock_guard<std::mutex> lk(mu_);
        purgeExpiredLocked(now);

        std::optional<AccessTokenRecord> matched;
        for (const auto& [storedKey, rec] : accessTokens_) {
            (void)storedKey;
            if (CRYPTO_memcmp(rec.tokenHash.data(), tokenDigest.data(), tokenDigest.size()) == 0) {
                matched = rec;
                break;
            }
        }
        if (!matched.has_value() || matched->revoked) {
            res.errorDescription = "Invalid or revoked bearer token";
            return res;
        }
        if (matched->expiresAt <= now) {
            res.errorDescription = "Bearer token expired";
            return res;
        }
        if (!tctx.expectedResource.empty() && matched->audience != tctx.expectedResource) {
            res.errorDescription = "Token audience does not match resource";
            return res;
        }
        if (matched->issuer != expectedIssuer) {
            res.errorDescription = "Token issuer mismatch";
            return res;
        }

        res.status = Status::Valid;
        res.principal.authenticated = true;
        res.principal.subject = matched->subject;
        res.principal.clientId = matched->clientId;
        res.principal.issuer = matched->issuer;
        res.principal.audience = matched->audience;
        res.principal.scopes = matched->scopes;
        res.principal.expiresAt = matched->expiresAt;
        return res;
    }

    std::vector<slimr::CustomHttpRoute> routes(bool includeProtectedResourceMetadata = false) {
        std::vector<slimr::CustomHttpRoute> out;

        if (includeProtectedResourceMetadata) {
            out.push_back(slimr::CustomHttpRoute{
                .method = "GET",
                .path = "/.well-known/oauth-protected-resource",
                .enforceSameOrigin = true,
                .handler = [this](const slimr::HttpRouteRequest& req) { return handleProtectedResourceMetadata(req); }
            });
        }

        out.push_back(slimr::CustomHttpRoute{
            .method = "GET",
            .path = "/.well-known/oauth-authorization-server",
            .enforceSameOrigin = true,
            .handler = [this](const slimr::HttpRouteRequest& req) { return handleMetadata(req); }
        });

        out.push_back(slimr::CustomHttpRoute{
            .method = "POST",
            .path = "/register",
            .enforceSameOrigin = true,
            .handler = [this](const slimr::HttpRouteRequest& req) { return handleRegister(req); }
        });

        out.push_back(slimr::CustomHttpRoute{
            .method = "GET",
            .path = "/authorize",
            .enforceSameOrigin = true,
            .handler = [this](const slimr::HttpRouteRequest& req) { return handleAuthorizeGet(req); }
        });

        out.push_back(slimr::CustomHttpRoute{
            .method = "GET",
            .path = "/authorize/qr",
            .enforceSameOrigin = true,
            .handler = [this](const slimr::HttpRouteRequest& req) { return handleAuthorizeQr(req); }
        });

        out.push_back(slimr::CustomHttpRoute{
            .method = "POST",
            .path = "/authorize/approve",
            .enforceSameOrigin = true,
            .handler = [this](const slimr::HttpRouteRequest& req) { return handleAuthorizeApprove(req); }
        });

        out.push_back(slimr::CustomHttpRoute{
            .method = "POST",
            .path = "/authorize/cancel",
            .enforceSameOrigin = true,
            .handler = [this](const slimr::HttpRouteRequest& req) { return handleAuthorizeCancel(req); }
        });

        out.push_back(slimr::CustomHttpRoute{
            .method = "POST",
            .path = "/token",
            .enforceSameOrigin = true,
            .handler = [this](const slimr::HttpRouteRequest& req) { return handleToken(req); }
        });

        out.push_back(slimr::CustomHttpRoute{
            .method = "POST",
            .path = "/revoke",
            .enforceSameOrigin = true,
            .handler = [this](const slimr::HttpRouteRequest& req) { return handleRevoke(req); }
        });

        return out;
    }

private:
    struct ClientRecord {
        std::string clientId;
        std::string clientName;
        std::vector<std::string> redirectUris;
        std::vector<std::string> scopes;
        std::chrono::system_clock::time_point expiresAt;
    };

    struct AuthTxRecord {
        std::string txId;
        std::string csrfToken;
        std::string csrfCookie;
        std::string clientId;
        std::string clientName;
        std::string redirectUri;
        std::string codeChallenge;
        std::string resource;
        std::string state;
        std::string baseUrl;
        std::vector<std::string> scopes;
        std::chrono::system_clock::time_point expiresAt;
    };

    struct AuthCodeRecord {
        std::string code;
        std::string clientId;
        std::string redirectUri;
        std::string codeChallenge;
        std::string resource;
        std::string issuer;
        std::string subject;
        std::vector<std::string> scopes;
        std::chrono::system_clock::time_point expiresAt;
    };

    struct AccessTokenRecord {
        std::array<uint8_t, 32> tokenHash{};
        std::string grantId;
        std::string clientId;
        std::string issuer;
        std::string audience;
        std::string subject;
        std::vector<std::string> scopes;
        std::chrono::system_clock::time_point expiresAt;
        bool revoked{false};
    };

    struct RefreshTokenRecord {
        std::array<uint8_t, 32> tokenHash{};
        std::string grantId;
        std::string clientId;
        std::string issuer;
        std::string audience;
        std::string subject;
        std::vector<std::string> scopes;
        std::chrono::system_clock::time_point expiresAt;
        bool used{false};
        bool revoked{false};
    };

    static constexpr size_t kMaxClients = 256;
    static constexpr size_t kMaxTxs = 256;
    static constexpr size_t kMaxCodes = 256;
    static constexpr size_t kMaxTokens = 1024;

    void purgeExpiredLocked(std::chrono::system_clock::time_point now) {
        std::erase_if(clients_, [&](const auto& kv) { return kv.second.expiresAt <= now; });
        std::erase_if(authTxs_, [&](const auto& kv) { return kv.second.expiresAt <= now; });
        std::erase_if(authCodes_, [&](const auto& kv) { return kv.second.expiresAt <= now; });
        std::erase_if(accessTokens_, [&](const auto& kv) { return kv.second.revoked || kv.second.expiresAt <= now; });
        std::erase_if(refreshTokens_, [&](const auto& kv) { return kv.second.revoked || kv.second.expiresAt <= now; });
    }

    slimr::HttpRouteResponse handleProtectedResourceMetadata(const slimr::HttpRouteRequest& req) const {
        std::string base = oauth_detail::derive_base_url_from_request(req, cfg_.defaultBaseUrl);
        slimr::OAuthResourceMetadata md;
        md.resourceName = "WhatsApp MCP";
        md.authorizationServers = {base};
        md.scopesSupported = {"whatsapp:read", "whatsapp:write"};
        auto resp = slimr::HttpRouteResponse::json(200, md.toJson(base + cfg_.mcpPath));
        oauth_detail::add_security_headers(resp, "");
        return resp;
    }

    slimr::HttpRouteResponse handleMetadata(const slimr::HttpRouteRequest& req) const {
        std::string base = oauth_detail::derive_base_url_from_request(req, cfg_.defaultBaseUrl);
        slimr::Json j = slimr::json_obj();
        j["issuer"] = base;
        j["authorization_endpoint"] = base + "/authorize";
        j["token_endpoint"] = base + "/token";
        j["registration_endpoint"] = base + "/register";
        j["revocation_endpoint"] = base + "/revoke";
        j["response_types_supported"] = slimr::Json::array_t{"code"};
        j["grant_types_supported"] = slimr::Json::array_t{"authorization_code", "refresh_token"};
        j["code_challenge_methods_supported"] = slimr::Json::array_t{"S256"};
        j["token_endpoint_auth_methods_supported"] = slimr::Json::array_t{"none"};
        j["scopes_supported"] = slimr::Json::array_t{"whatsapp:read", "whatsapp:write"};
        auto resp = slimr::HttpRouteResponse::json(200, j);
        oauth_detail::add_security_headers(resp, "");
        return resp;
    }

    static slimr::HttpRouteResponse jsonError(int status, std::string_view code, std::string_view desc) {
        slimr::Json j = slimr::json_obj();
        j["error"] = std::string(code);
        j["error_description"] = std::string(desc);
        auto resp = slimr::HttpRouteResponse::json(status, j);
        oauth_detail::add_security_headers(resp, "");
        return resp;
    }

    static slimr::HttpRouteResponse htmlError(int status, std::string_view title, std::string_view detail) {
        std::string body = "<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"utf-8\">"
                           "<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">"
                           "<title>" + oauth_detail::html_escape(title) + "</title>"
                           "<style>body{font-family:-apple-system,BlinkMacSystemFont,sans-serif;max-width:520px;margin:48px auto;padding:0 20px;color:#111;background:#fafafa}"
                           ".card{background:#fff;border:1px solid #e5e7eb;border-radius:12px;padding:24px;box-shadow:0 2px 8px rgba(0,0,0,.04)}"
                           "h1{font-size:18px;margin:0 0 10px}p{color:#4b5563;line-height:1.5;margin:0}</style>"
                           "</head><body><div class=\"card\"><h1>" + oauth_detail::html_escape(title) +
                           "</h1><p>" + oauth_detail::html_escape(detail) + "</p></div></body></html>";
        auto resp = slimr::HttpRouteResponse::html(status, std::move(body));
        oauth_detail::add_security_headers(resp, "default-src 'none'; style-src 'unsafe-inline'");
        return resp;
    }

    slimr::HttpRouteResponse handleRegister(const slimr::HttpRouteRequest& req) {
        auto parsed = slimr::parse_json(req.body);
        if (!parsed || !parsed->is_object()) {
            return jsonError(400, "invalid_client_metadata", "Expected JSON object body");
        }
        const auto& j = *parsed;
        if (!j.contains("redirect_uris") || !j["redirect_uris"].is_array() || j["redirect_uris"].get_array().empty()) {
            return jsonError(400, "invalid_redirect_uri", "redirect_uris must be a non-empty array");
        }

        std::vector<std::string> redirectUris;
        for (const auto& item : j["redirect_uris"].get_array()) {
            if (!item.is_string()) {
                return jsonError(400, "invalid_redirect_uri", "redirect_uris entries must be strings");
            }
            const std::string& uri = item.get<std::string>();
            if (!oauth_detail::is_valid_loopback_redirect_uri(uri)) {
                return jsonError(400, "invalid_redirect_uri", "Only loopback HTTP redirect_uris are allowed");
            }
            redirectUris.push_back(uri);
        }

        std::string clientName = "MCP Client";
        if (j.contains("client_name") && j["client_name"].is_string()) {
            std::string rawName = j["client_name"].get<std::string>();
            if (!rawName.empty() && !oauth_detail::has_control_chars(rawName)) {
                clientName = rawName.substr(0, 128);
            }
        }

        std::vector<std::string> scopes = {"whatsapp:read", "whatsapp:write"};
        if (j.contains("scope") && j["scope"].is_string() && !j["scope"].get<std::string>().empty()) {
            auto reqScopes = oauth_detail::parse_scopes(j["scope"].get<std::string>());
            if (reqScopes.empty()) {
                return jsonError(400, "invalid_scope", "Empty scope");
            }
            for (const auto& s : reqScopes) {
                if (!oauth_detail::is_supported_scope(s)) {
                    return jsonError(400, "invalid_scope", "Unsupported scope: " + s);
                }
            }
            scopes = std::move(reqScopes);
        }

        const auto now = std::chrono::system_clock::now();
        std::string clientId = "wa_" + oauth_detail::random_token(24);

        {
            std::lock_guard<std::mutex> lk(mu_);
            purgeExpiredLocked(now);
            if (clients_.size() >= kMaxClients) {
                return jsonError(429, "temporarily_unavailable", "Registered client capacity reached");
            }
            clients_[clientId] = ClientRecord{
                .clientId = clientId,
                .clientName = clientName,
                .redirectUris = redirectUris,
                .scopes = scopes,
                .expiresAt = now + std::chrono::seconds(cfg_.clientTtlSeconds),
            };
        }

        slimr::Json out = slimr::json_obj();
        out["client_id"] = clientId;
        out["client_name"] = clientName;
        slimr::Json::array_t uriArr;
        for (const auto& u : redirectUris) uriArr.emplace_back(u);
        out["redirect_uris"] = slimr::json_arr(std::move(uriArr));
        out["grant_types"] = slimr::Json::array_t{"authorization_code", "refresh_token"};
        out["response_types"] = slimr::Json::array_t{"code"};
        out["token_endpoint_auth_method"] = "none";
        out["scope"] = oauth_detail::join_scopes(scopes);

        auto resp = slimr::HttpRouteResponse::json(201, out);
        oauth_detail::add_security_headers(resp, "");
        return resp;
    }

    slimr::HttpRouteResponse handleAuthorizeGet(const slimr::HttpRouteRequest& req) {
        std::string clientId = req.query("client_id").value_or("");
        std::string redirectUri = req.query("redirect_uri").value_or("");
        std::string responseType = req.query("response_type").value_or("");
        std::string codeChallenge = req.query("code_challenge").value_or("");
        std::string codeChallengeMethod = req.query("code_challenge_method").value_or("");
        std::string resource = req.query("resource").value_or("");
        std::string state = req.query("state").value_or("");
        std::string rawScope = req.query("scope").value_or("");

        if (clientId.empty() || redirectUri.empty()) {
            return htmlError(400, "Invalid Authorization Request", "client_id and redirect_uri are required.");
        }
        if (oauth_detail::has_control_chars(state) || state.size() > 512) {
            return htmlError(400, "Invalid Authorization Request", "Invalid state parameter.");
        }

        const auto now = std::chrono::system_clock::now();
        ClientRecord client;
        {
            std::lock_guard<std::mutex> lk(mu_);
            purgeExpiredLocked(now);
            auto it = clients_.find(clientId);
            if (it == clients_.end()) {
                return htmlError(400, "Unknown Client", "Unregistered or expired client_id.");
            }
            client = it->second;
        }

        // Exact redirect_uri match prevents open redirects.
        if (std::find(client.redirectUris.begin(), client.redirectUris.end(), redirectUri) == client.redirectUris.end()) {
            return htmlError(400, "Invalid Redirect URI", "redirect_uri does not match registered client redirect URIs.");
        }

        if (responseType != "code") {
            return htmlError(400, "Unsupported Response Type", "response_type must be 'code'.");
        }
        if (codeChallengeMethod != "S256" || !oauth_detail::is_valid_b64url_challenge(codeChallenge)) {
            return htmlError(400, "Invalid PKCE Challenge", "code_challenge_method must be S256 with a valid challenge.");
        }

        std::string base = oauth_detail::derive_base_url_from_request(req, cfg_.defaultBaseUrl);
        std::string expectedResource = base + cfg_.mcpPath;
        if (resource != expectedResource) {
            return htmlError(400, "Invalid Resource Target", "resource must match this MCP endpoint.");
        }

        std::vector<std::string> scopes = client.scopes;
        if (!rawScope.empty()) {
            auto parsedScopes = oauth_detail::parse_scopes(rawScope);
            if (parsedScopes.empty()) {
                return htmlError(400, "Invalid Scope", "Requested scope is empty.");
            }
            for (const auto& s : parsedScopes) {
                if (!oauth_detail::is_supported_scope(s)) {
                    return htmlError(400, "Invalid Scope", "Unsupported scope requested.");
                }
            }
            scopes = std::move(parsedScopes);
        }

        std::string txId = oauth_detail::random_token(24);
        std::string csrfToken = oauth_detail::random_token(24);
        std::string csrfCookie = oauth_detail::random_token(24);

        {
            std::lock_guard<std::mutex> lk(mu_);
            if (authTxs_.size() >= kMaxTxs) {
                return htmlError(429, "Server Busy", "Too many pending authorization requests.");
            }
            authTxs_[txId] = AuthTxRecord{
                .txId = txId,
                .csrfToken = csrfToken,
                .csrfCookie = csrfCookie,
                .clientId = client.clientId,
                .clientName = client.clientName,
                .redirectUri = redirectUri,
                .codeChallenge = codeChallenge,
                .resource = expectedResource,
                .state = state,
                .baseUrl = base,
                .scopes = scopes,
                .expiresAt = now + std::chrono::seconds(cfg_.authTxTtlSeconds),
            };
        }

        PairStatus pairStatus = daemon_pair_status(cfg_.daemonSocketPath);
        std::string scopeStr = oauth_detail::join_scopes(scopes);

        std::ostringstream html;
        html << "<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"utf-8\">"
             << "<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">"
             << "<title>Connect WhatsApp MCP</title>"
             << "<style>"
             << "body{font-family:-apple-system,BlinkMacSystemFont,sans-serif;max-width:520px;margin:40px auto;padding:0 20px;color:#111827;background:#f8fafc}"
             << ".card{background:#fff;border:1px solid #e2e8f0;border-radius:14px;padding:26px;box-shadow:0 4px 16px rgba(15,23,42,.06)}"
             << "h1{font-size:20px;margin:0 0 8px}p{color:#475569;line-height:1.5;margin:0 0 16px}"
             << ".meta{background:#f1f5f9;border-radius:10px;padding:12px 14px;font-size:14px;margin-bottom:18px}"
             << ".meta div{margin:4px 0}code{background:#e2e8f0;padding:2px 6px;border-radius:5px;font-size:13px}"
             << ".qr-box{text-align:center;padding:16px;border:1px dashed #cbd5e1;border-radius:12px;margin-bottom:18px;background:#fff}"
             << ".qr-box img{width:240px;height:240px;display:block;margin:0 auto 10px}"
             << ".actions{display:flex;gap:10px;flex-wrap:wrap;margin-top:16px}"
             << "button{cursor:pointer;border:none;border-radius:9px;padding:10px 18px;font-size:14px;font-weight:600}"
             << ".primary{background:#16a34a;color:#fff}.secondary{background:#e2e8f0;color:#1e293b}.danger{background:#fee2e2;color:#991b1b}"
             << ".hidden{display:none}"
             << "</style></head><body><div class=\"card\">"
             << "<h1>Authorize WhatsApp MCP Access</h1>"
             << "<div class=\"meta\">"
             << "<div><strong>Client:</strong> " << oauth_detail::html_escape(client.clientName) << "</div>"
             << "<div><strong>Scopes:</strong> <code>" << oauth_detail::html_escape(scopeStr) << "</code></div>"
             << "<div><strong>Resource:</strong> <code>" << oauth_detail::html_escape(expectedResource) << "</code></div>";

        if (!pairStatus.available) {
            html << "<div><strong>Daemon:</strong> Unavailable</div></div>"
                 << "<p>The <code>whatsapp-client</code> sync daemon is not running or <code>daemon.sock</code> was not found. Start <code>whatsapp-client</code> and reload this page.</p>"
                 << "<form method=\"POST\" action=\"/authorize/cancel\" class=\"actions\">"
                 << "<input type=\"hidden\" name=\"tx\" value=\"" << oauth_detail::html_escape(txId) << "\">"
                 << "<input type=\"hidden\" name=\"csrf\" value=\"" << oauth_detail::html_escape(csrfToken) << "\">"
                 << "<button type=\"submit\" class=\"danger\">Cancel</button>"
                 << "</form></div></body></html>";
        } else if (pairStatus.paired) {
            html << "<div><strong>WhatsApp Account:</strong> <code id=\"wa-jid\">"
                 << oauth_detail::html_escape(pairStatus.jid) << "</code></div></div>"
                 << "<p>Your WhatsApp session is already paired. Review the permissions above and confirm to grant access.</p>"
                 << "<form method=\"POST\" action=\"/authorize/approve\" class=\"actions\">"
                 << "<input type=\"hidden\" name=\"tx\" value=\"" << oauth_detail::html_escape(txId) << "\">"
                 << "<input type=\"hidden\" name=\"csrf\" value=\"" << oauth_detail::html_escape(csrfToken) << "\">"
                 << "<button type=\"submit\" name=\"action\" value=\"approve\" class=\"primary\">Approve Connection</button>"
                 << "<button type=\"submit\" name=\"action\" value=\"deny\" class=\"secondary\">Deny</button>"
                 << "</form></div></body></html>";
        } else {
            html << "<div><strong>WhatsApp Account:</strong> <code id=\"wa-jid\">Not paired yet</code></div></div>"
                 << "<div id=\"qr-panel\" class=\"qr-box\">"
                 << "<img id=\"qr-img\" alt=\"WhatsApp Pairing QR Code\" class=\"hidden\">"
                 << "<p id=\"qr-status\">Requesting QR code from WhatsApp sync daemon...</p>"
                 << "<button type=\"button\" id=\"qr-retry\" class=\"secondary hidden\">Retry QR</button>"
                 << "</div>"
                 << "<div id=\"approve-panel\" class=\"hidden\">"
                 << "<p>WhatsApp paired! Confirm below to finish connecting this client.</p>"
                 << "<form method=\"POST\" action=\"/authorize/approve\" class=\"actions\">"
                 << "<input type=\"hidden\" name=\"tx\" value=\"" << oauth_detail::html_escape(txId) << "\">"
                 << "<input type=\"hidden\" name=\"csrf\" value=\"" << oauth_detail::html_escape(csrfToken) << "\">"
                 << "<button type=\"submit\" name=\"action\" value=\"approve\" class=\"primary\">Approve Connection</button>"
                 << "<button type=\"submit\" name=\"action\" value=\"deny\" class=\"secondary\">Deny</button>"
                 << "</form></div>"
                 << "<form id=\"cancel-form\" method=\"POST\" action=\"/authorize/cancel\" class=\"actions\">"
                 << "<input type=\"hidden\" name=\"tx\" value=\"" << oauth_detail::html_escape(txId) << "\">"
                 << "<input type=\"hidden\" name=\"csrf\" value=\"" << oauth_detail::html_escape(csrfToken) << "\">"
                 << "<button type=\"submit\" class=\"danger\">Cancel Pairing</button>"
                 << "</form>"
                 << "<script>(function(){"
                 << "const tx=" << slimr::write_json(slimr::Json(txId)) << ";"
                 << "const img=document.getElementById('qr-img');"
                 << "const st=document.getElementById('qr-status');"
                 << "const retry=document.getElementById('qr-retry');"
                 << "const qrPanel=document.getElementById('qr-panel');"
                 << "const appPanel=document.getElementById('approve-panel');"
                 << "const cancelForm=document.getElementById('cancel-form');"
                 << "const jidEl=document.getElementById('wa-jid');"
                 << "let timer=null;"
                 << "async function poll(){"
                 << "try{const r=await fetch('/authorize/qr?tx='+encodeURIComponent(tx),{cache:'no-store'});"
                 << "const d=await r.json();"
                 << "if(d.paired||d.status==='already_paired'){"
                 << "qrPanel.classList.add('hidden');cancelForm.classList.add('hidden');appPanel.classList.remove('hidden');"
                 << "if(d.jid)jidEl.textContent=d.jid;return;"
                 << "}"
                 << "if(d.status==='available'&&d.svg_b64){"
                 << "img.src='data:image/svg+xml;base64,'+d.svg_b64;img.classList.remove('hidden');retry.classList.add('hidden');"
                 << "st.textContent='Open WhatsApp -> Linked Devices -> Link a Device and scan this QR code (expires in '+(d.expires_in_s||20)+'s).';"
                 << "}else if(d.status==='timeout'){"
                 << "img.classList.add('hidden');retry.classList.remove('hidden');"
                 << "st.textContent='QR code expired before scanning. Click Retry QR to generate a fresh code.';return;"
                 << "}else if(d.status==='daemon_unavailable'){"
                 << "img.classList.add('hidden');st.textContent='Sync daemon unavailable.';return;"
                 << "}else{st.textContent='Waiting for QR code from WhatsApp...';}"
                 << "}catch(e){st.textContent='Waiting for local server...';}"
                 << "timer=setTimeout(poll,1500);"
                 << "}"
                 << "retry.addEventListener('click',function(){retry.classList.add('hidden');poll();});"
                 << "poll();"
                 << "})();</script>"
                 << "</div></body></html>";
        }

        auto resp = slimr::HttpRouteResponse::html(200, html.str());
        resp.headers["Set-Cookie"] = "wa_oauth_csrf=" + csrfCookie + "; HttpOnly; SameSite=Strict; Path=/authorize";
        oauth_detail::add_security_headers(
            resp,
            "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src 'self'; form-action 'self'");
        return resp;
    }

    slimr::HttpRouteResponse handleAuthorizeQr(const slimr::HttpRouteRequest& req) {
        std::string txId = req.query("tx").value_or("");
        if (txId.empty()) {
            return jsonError(400, "invalid_request", "Missing tx parameter");
        }
        const auto now = std::chrono::system_clock::now();
        {
            std::lock_guard<std::mutex> lk(mu_);
            purgeExpiredLocked(now);
            if (authTxs_.find(txId) == authTxs_.end()) {
                return jsonError(404, "invalid_request", "Unknown or expired authorization transaction");
            }
        }

        PairStatus st = daemon_pair_status(cfg_.daemonSocketPath);
        if (!st.available) {
            slimr::Json j = slimr::json_obj();
            j["status"] = "daemon_unavailable";
            j["paired"] = false;
            auto resp = slimr::HttpRouteResponse::json(200, j);
            oauth_detail::add_security_headers(resp, "");
            return resp;
        }
        if (st.paired) {
            slimr::Json j = slimr::json_obj();
            j["status"] = "already_paired";
            j["paired"] = true;
            j["connected"] = st.connected;
            j["jid"] = st.jid;
            auto resp = slimr::HttpRouteResponse::json(200, j);
            oauth_detail::add_security_headers(resp, "");
            return resp;
        }

        PairQrState qr = daemon_pair_qr(cfg_.daemonSocketPath);
        if (!qr.available) {
            slimr::Json j = slimr::json_obj();
            j["status"] = "daemon_unavailable";
            j["paired"] = false;
            auto resp = slimr::HttpRouteResponse::json(200, j);
            oauth_detail::add_security_headers(resp, "");
            return resp;
        }

        slimr::Json j = slimr::json_obj();
        j["status"] = qr.status.empty() ? "waiting" : qr.status;
        j["paired"] = qr.paired;
        if (qr.paired || qr.status == "already_paired") {
            PairStatus afterSt = daemon_pair_status(cfg_.daemonSocketPath);
            j["status"] = "already_paired";
            j["paired"] = true;
            j["jid"] = afterSt.jid;
        } else if (qr.status == "available" && !qr.qr.empty()) {
            std::string svg = qr_to_svg(qr.qr);
            j["svg_b64"] = slimr::mcp_detail::base64_encode(svg);
            j["expires_in_s"] = qr.expiresInS;
        }
        auto resp = slimr::HttpRouteResponse::json(200, j);
        oauth_detail::add_security_headers(resp, "");
        return resp;
    }

    static std::map<std::string, std::string> parseRequestParams(const slimr::HttpRouteRequest& req) {
        auto ct = req.header("content-type").value_or("");
        if (oauth_detail::ascii_lower(ct).find("application/json") != std::string::npos) {
            std::map<std::string, std::string> out;
            auto parsed = slimr::parse_json(req.body);
            if (parsed && parsed->is_object()) {
                for (const auto& [k, v] : parsed->get_object()) {
                    if (v.is_string()) out[k] = v.get<std::string>();
                }
            }
            return out;
        }
        return oauth_detail::parse_form_urlencoded(req.body);
    }

    slimr::HttpRouteResponse handleAuthorizeApprove(const slimr::HttpRouteRequest& req) {
        auto params = parseRequestParams(req);
        const std::string& txId = params["tx"];
        const std::string& csrf = params["csrf"];
        const std::string& action = params["action"];

        if (txId.empty() || csrf.empty()) {
            return htmlError(400, "Invalid Request", "Missing authorization transaction or CSRF token.");
        }

        const auto now = std::chrono::system_clock::now();
        AuthTxRecord tx;
        {
            std::lock_guard<std::mutex> lk(mu_);
            purgeExpiredLocked(now);
            auto it = authTxs_.find(txId);
            if (it == authTxs_.end()) {
                return htmlError(400, "Expired or Replayed Request", "Authorization transaction is invalid, expired, or already used.");
            }
            if (!oauth_detail::constant_time_eq(csrf, it->second.csrfToken)) {
                return htmlError(403, "CSRF Check Failed", "Invalid CSRF token.");
            }
            if (auto cookieHdr = req.header("cookie")) {
                auto cval = oauth_detail::extract_cookie_value(*cookieHdr, "wa_oauth_csrf");
                if (cval.has_value() && !oauth_detail::constant_time_eq(*cval, it->second.csrfCookie)) {
                    return htmlError(403, "CSRF Check Failed", "Invalid CSRF cookie.");
                }
            }
            if (action != "deny" && action != "approve") {
                return htmlError(400, "Invalid Action", "Unsupported authorization action.");
            }
            tx = it->second;
        }

        if (action == "deny") {
            {
                std::lock_guard<std::mutex> lk(mu_);
                authTxs_.erase(txId);
            }
            std::string loc = oauth_detail::append_redirect_params(
                tx.redirectUri, {{"error", "access_denied"}, {"state", tx.state}});
            auto resp = slimr::HttpRouteResponse::redirect(loc, 302);
            oauth_detail::add_security_headers(resp, "");
            return resp;
        }

        PairStatus pairStatus = daemon_pair_status(cfg_.daemonSocketPath);
        if (!pairStatus.available || !pairStatus.paired) {
            return htmlError(409, "WhatsApp Not Paired", "Cannot approve connection before WhatsApp pairing completes.");
        }

        {
            std::lock_guard<std::mutex> lk(mu_);
            auto it = authTxs_.find(txId);
            if (it == authTxs_.end()) {
                return htmlError(400, "Expired or Replayed Request", "Authorization transaction is invalid, expired, or already used.");
            }
            authTxs_.erase(it);
        }

        std::string code = oauth_detail::random_token(32);
        {
            std::lock_guard<std::mutex> lk(mu_);
            if (authCodes_.size() >= kMaxCodes) {
                return htmlError(429, "Server Busy", "Authorization code store is full.");
            }
            authCodes_[code] = AuthCodeRecord{
                .code = code,
                .clientId = tx.clientId,
                .redirectUri = tx.redirectUri,
                .codeChallenge = tx.codeChallenge,
                .resource = tx.resource,
                .issuer = tx.baseUrl,
                .subject = pairStatus.jid,
                .scopes = tx.scopes,
                .expiresAt = now + std::chrono::seconds(cfg_.authCodeTtlSeconds),
            };
        }

        std::string loc = oauth_detail::append_redirect_params(
            tx.redirectUri, {{"code", code}, {"state", tx.state}});
        auto resp = slimr::HttpRouteResponse::redirect(loc, 302);
        oauth_detail::add_security_headers(resp, "");
        return resp;
    }

    slimr::HttpRouteResponse handleAuthorizeCancel(const slimr::HttpRouteRequest& req) {
        auto params = parseRequestParams(req);
        const std::string& txId = params["tx"];
        const std::string& csrf = params["csrf"];
        if (txId.empty() || csrf.empty()) {
            return htmlError(400, "Invalid Request", "Missing transaction or CSRF token.");
        }

        const auto now = std::chrono::system_clock::now();
        AuthTxRecord tx;
        {
            std::lock_guard<std::mutex> lk(mu_);
            purgeExpiredLocked(now);
            auto it = authTxs_.find(txId);
            if (it == authTxs_.end()) {
                return htmlError(400, "Expired Request", "Authorization transaction is invalid or expired.");
            }
            if (!oauth_detail::constant_time_eq(csrf, it->second.csrfToken)) {
                return htmlError(403, "CSRF Check Failed", "Invalid CSRF token.");
            }
            tx = std::move(it->second);
            authTxs_.erase(it);
        }

        // Only cancels an unpaired in-progress QR session; daemon refuses if already paired.
        daemon_pair_cancel(cfg_.daemonSocketPath);

        std::string loc = oauth_detail::append_redirect_params(
            tx.redirectUri, {{"error", "access_denied"}, {"state", tx.state}});
        auto resp = slimr::HttpRouteResponse::redirect(loc, 302);
        oauth_detail::add_security_headers(resp, "");
        return resp;
    }

    slimr::HttpRouteResponse handleToken(const slimr::HttpRouteRequest& req) {
        auto params = parseRequestParams(req);
        const std::string& grantType = params["grant_type"];
        const auto now = std::chrono::system_clock::now();

        if (grantType == "authorization_code") {
            const std::string& code = params["code"];
            const std::string& codeVerifier = params["code_verifier"];
            const std::string& clientId = params["client_id"];
            const std::string& redirectUri = params["redirect_uri"];
            const std::string& resource = params["resource"];

            if (code.empty() || codeVerifier.empty() || clientId.empty() || redirectUri.empty()) {
                return jsonError(400, "invalid_request", "Missing required token parameters");
            }
            if (!oauth_detail::is_valid_pkce_verifier(codeVerifier)) {
                return jsonError(400, "invalid_grant", "Malformed PKCE code_verifier");
            }

            AuthCodeRecord rec;
            {
                std::lock_guard<std::mutex> lk(mu_);
                purgeExpiredLocked(now);
                auto it = authCodes_.find(code);
                if (it == authCodes_.end()) {
                    return jsonError(400, "invalid_grant", "Unknown, expired, or already used authorization code");
                }
                rec = std::move(it->second);
                authCodes_.erase(it); // Single-use enforcement.
            }

            if (rec.expiresAt <= now) {
                return jsonError(400, "invalid_grant", "Authorization code expired");
            }
            if (!oauth_detail::constant_time_eq(clientId, rec.clientId) || redirectUri != rec.redirectUri) {
                return jsonError(400, "invalid_grant", "client_id or redirect_uri mismatch");
            }
            if (resource != rec.resource) {
                return jsonError(400, "invalid_target", "resource parameter does not match authorized resource");
            }

            std::string computedChallenge = oauth_detail::sha256_b64url(codeVerifier);
            if (!oauth_detail::constant_time_eq(computedChallenge, rec.codeChallenge)) {
                return jsonError(400, "invalid_grant", "PKCE S256 verification failed");
            }

            return issueTokens(rec.clientId, rec.issuer, rec.resource, rec.subject, rec.scopes, "", now);
        }

        if (grantType == "refresh_token") {
            const std::string& rawRefresh = params["refresh_token"];
            const std::string& clientId = params["client_id"];
            const std::string& resource = params["resource"];
            if (rawRefresh.empty() || clientId.empty()) {
                return jsonError(400, "invalid_request", "Missing refresh_token or client_id");
            }

            auto refreshDigest = oauth_detail::sha256_digest(rawRefresh);
            std::string matchedKey;
            RefreshTokenRecord rec;
            {
                std::lock_guard<std::mutex> lk(mu_);
                purgeExpiredLocked(now);
                for (auto& [k, v] : refreshTokens_) {
                    if (CRYPTO_memcmp(v.tokenHash.data(), refreshDigest.data(), refreshDigest.size()) == 0) {
                        matchedKey = k;
                        rec = v;
                        if (v.used || v.revoked) {
                            // Refresh token replay detected: revoke entire grant family.
                            for (auto& [ak, av] : accessTokens_) {
                                if (av.grantId == v.grantId) av.revoked = true;
                            }
                            for (auto& [rk, rv] : refreshTokens_) {
                                if (rv.grantId == v.grantId) rv.revoked = true;
                            }
                            return jsonError(400, "invalid_grant", "Refresh token has been revoked or replayed");
                        }
                        v.used = true;
                        break;
                    }
                }
                if (matchedKey.empty()) {
                    return jsonError(400, "invalid_grant", "Invalid or expired refresh token");
                }
            }

            if (rec.expiresAt <= now || !oauth_detail::constant_time_eq(clientId, rec.clientId)) {
                return jsonError(400, "invalid_grant", "Refresh token expired or client mismatch");
            }
            if (!resource.empty() && resource != rec.audience) {
                return jsonError(400, "invalid_target", "resource parameter does not match authorized resource");
            }

            return issueTokens(rec.clientId, rec.issuer, rec.audience, rec.subject, rec.scopes, rec.grantId, now);
        }

        return jsonError(400, "unsupported_grant_type", "Supported grant types: authorization_code, refresh_token");
    }

    slimr::HttpRouteResponse issueTokens(const std::string& clientId,
                                         const std::string& issuer,
                                         const std::string& audience,
                                         const std::string& subject,
                                         const std::vector<std::string>& scopes,
                                         std::string grantId,
                                         std::chrono::system_clock::time_point now) {
        if (grantId.empty()) {
            grantId = oauth_detail::random_token(16);
        }
        std::string rawAccess = oauth_detail::random_token(32);
        std::string rawRefresh = oauth_detail::random_token(32);
        auto accessHash = oauth_detail::sha256_digest(rawAccess);
        auto refreshHash = oauth_detail::sha256_digest(rawRefresh);
        std::string accessKey = oauth_detail::b64url_encode(accessHash.data(), accessHash.size());
        std::string refreshKey = oauth_detail::b64url_encode(refreshHash.data(), refreshHash.size());

        {
            std::lock_guard<std::mutex> lk(mu_);
            purgeExpiredLocked(now);
            if (accessTokens_.size() >= kMaxTokens || refreshTokens_.size() >= kMaxTokens) {
                return jsonError(503, "temporarily_unavailable", "Token capacity reached");
            }
            accessTokens_[accessKey] = AccessTokenRecord{
                .tokenHash = accessHash,
                .grantId = grantId,
                .clientId = clientId,
                .issuer = issuer,
                .audience = audience,
                .subject = subject,
                .scopes = scopes,
                .expiresAt = now + std::chrono::seconds(cfg_.accessTokenTtlSeconds),
                .revoked = false,
            };
            refreshTokens_[refreshKey] = RefreshTokenRecord{
                .tokenHash = refreshHash,
                .grantId = grantId,
                .clientId = clientId,
                .issuer = issuer,
                .audience = audience,
                .subject = subject,
                .scopes = scopes,
                .expiresAt = now + std::chrono::seconds(cfg_.refreshTokenTtlSeconds),
                .used = false,
                .revoked = false,
            };
        }

        slimr::Json out = slimr::json_obj();
        out["access_token"] = rawAccess;
        out["token_type"] = "Bearer";
        out["expires_in"] = cfg_.accessTokenTtlSeconds;
        out["refresh_token"] = rawRefresh;
        out["scope"] = oauth_detail::join_scopes(scopes);

        auto resp = slimr::HttpRouteResponse::json(200, out);
        oauth_detail::add_security_headers(resp, "");
        return resp;
    }

    slimr::HttpRouteResponse handleRevoke(const slimr::HttpRouteRequest& req) {
        auto params = parseRequestParams(req);
        const std::string& token = params["token"];
        if (!token.empty()) {
            auto digest = oauth_detail::sha256_digest(token);
            std::lock_guard<std::mutex> lk(mu_);
            for (auto& [k, v] : accessTokens_) {
                (void)k;
                if (CRYPTO_memcmp(v.tokenHash.data(), digest.data(), digest.size()) == 0) {
                    v.revoked = true;
                }
            }
            for (auto& [k, v] : refreshTokens_) {
                (void)k;
                if (CRYPTO_memcmp(v.tokenHash.data(), digest.data(), digest.size()) == 0) {
                    v.revoked = true;
                    for (auto& [ak, av] : accessTokens_) {
                        if (av.grantId == v.grantId) av.revoked = true;
                    }
                }
            }
        }
        // RFC 7009: revocation returns 200 OK and never disconnects the underlying WhatsApp session.
        auto resp = slimr::HttpRouteResponse::json(200, slimr::json_obj());
        oauth_detail::add_security_headers(resp, "");
        return resp;
    }

    OAuthAsConfig cfg_;
    std::mutex mu_;
    std::map<std::string, ClientRecord> clients_;
    std::map<std::string, AuthTxRecord> authTxs_;
    std::map<std::string, AuthCodeRecord> authCodes_;
    std::map<std::string, AccessTokenRecord> accessTokens_;
    std::map<std::string, RefreshTokenRecord> refreshTokens_;
};

inline std::shared_ptr<OAuthAsServer> configure_whatsapp_http_oauth(slimr::HttpServerOptions& httpOpts,
                                                                    OAuthAsConfig asCfg,
                                                                    bool isFixedPort) {
    auto oauthServer = std::make_shared<OAuthAsServer>(asCfg);

    httpOpts.oauth.enabled = true;
    httpOpts.oauth.protectDiscover = true;
    httpOpts.oauth.defaultScopes = {"whatsapp:read"};
    httpOpts.oauth.metadata.scopesSupported = {"whatsapp:read", "whatsapp:write"};
    httpOpts.oauth.metadata.resourceName = "WhatsApp MCP";
    httpOpts.oauth.metadata.authorizationServers = {asCfg.defaultBaseUrl};
    if (isFixedPort) {
        httpOpts.oauth.expectedIssuers = {asCfg.defaultBaseUrl};
    } else {
        // On ephemeral port (--port 0), validateToken checks exact issuer derived from tctx.expectedResource.
        httpOpts.oauth.expectedIssuers.clear();
    }
    httpOpts.oauth.validateToken = [oauthServer](const slimr::TokenValidationContext& tctx) {
        return oauthServer->validateBearerToken(tctx);
    };

    for (auto& r : oauthServer->routes(/*includeProtectedResourceMetadata=*/!isFixedPort)) {
        httpOpts.customRoutes.push_back(std::move(r));
    }
    return oauthServer;
}

} // namespace whatsapp
