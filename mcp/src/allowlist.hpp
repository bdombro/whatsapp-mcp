#pragma once

#include <cstdlib>
#include <filesystem>
#include <stdexcept>
#include <string>
#include <vector>

namespace whatsapp {

inline bool check_location_under_roots(const std::filesystem::path& p,
                                       const std::vector<std::filesystem::path>& roots,
                                       const std::filesystem::path& home,
                                       const std::string& shown) {
    bool matchedRoot = false;
    for (const auto& root : roots) {
        std::error_code ec;
        auto rel = std::filesystem::relative(p, root, ec);
        if (ec || rel.empty() || rel == "." || rel.string().starts_with("..")) {
            continue;
        }

        matchedRoot = true;

        // Check if root is home and path is in Library
        if (!home.empty() && std::filesystem::equivalent(root, home, ec)) {
            auto firstPart = rel.begin() != rel.end() ? rel.begin()->string() : "";
            if (firstPart == "Library") {
                throw std::runtime_error("refusing to send files from ~/Library: " + shown);
            }
        }

        // Check each component for hidden folders / files (starting with .)
        for (const auto& part : rel) {
            std::string partStr = part.string();
            if (partStr.starts_with(".") && partStr != "." && partStr != "..") {
                throw std::runtime_error("refusing to send files from hidden folders or hidden files: " + shown);
            }
        }
        break;
    }

    if (!matchedRoot) {
        throw std::runtime_error("refusing to send files outside your home folder or temp dirs: " + shown);
    }
    return true;
}

/// Validates that mediaPath is an absolute path to a regular file located under $HOME or temp dirs,
/// and that neither the path nor its resolved symlink target points to ~/Library or any hidden folder/file.
inline std::string validate_sendable_path(const std::string& mediaPath) {
    std::filesystem::path p(mediaPath);
    if (!p.is_absolute()) {
        throw std::runtime_error("media path must be absolute: " + mediaPath);
    }

    std::error_code ec;
    auto canonical = std::filesystem::canonical(p, ec);
    if (ec || !std::filesystem::exists(canonical)) {
        throw std::runtime_error("error reading media file: " + mediaPath);
    }

    if (!std::filesystem::is_regular_file(canonical)) {
        throw std::runtime_error("media path is not a regular file: " + mediaPath);
    }

    std::vector<std::filesystem::path> roots;
    roots.push_back("/tmp");
    roots.push_back("/private/tmp");
    roots.push_back("/var/folders");
    roots.push_back("/private/var/folders");

    auto tempDir = std::filesystem::temp_directory_path(ec);
    if (!ec) {
        roots.push_back(tempDir);
        auto canTemp = std::filesystem::canonical(tempDir, ec);
        if (!ec) roots.push_back(canTemp);
    }

    std::filesystem::path homePath;
    const char* home = std::getenv("HOME");
    if (home && home[0] != '\0') {
        homePath = home;
        roots.push_back(homePath);
        auto canHome = std::filesystem::canonical(homePath, ec);
        if (!ec) roots.push_back(canHome);
    }

    // Check both given path (lexically normalized) and canonical target
    check_location_under_roots(p.lexically_normal(), roots, homePath, mediaPath);
    check_location_under_roots(canonical, roots, homePath, mediaPath);

    return canonical.string();
}

} // namespace whatsapp
