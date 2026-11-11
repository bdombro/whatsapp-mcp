#pragma once

#include "json.hpp"

#include <chrono>
#include <ctime>
#include <filesystem>
#include <fstream>
#include <iomanip>
#include <mutex>
#include <sstream>
#include <string>
#include <sys/stat.h>

namespace whatsapp {

inline std::mutex& audit_mutex() {
    static std::mutex mu;
    return mu;
}

inline void log_audit(const std::string& dataDir, const std::string& tool, const slimr::Json& args) {
    if (dataDir.empty()) return;
    try {
        std::lock_guard<std::mutex> lock(audit_mutex());
        std::filesystem::path dir(dataDir);
        std::filesystem::create_directories(dir);
        std::filesystem::path logFile = dir / "mcp-audit.log";

        // Current time RFC3339
        auto now = std::chrono::system_clock::now();
        std::time_t tt = std::chrono::system_clock::to_time_t(now);
        struct tm tm{};
        gmtime_r(&tt, &tm);
        char tsBuf[64];
        std::strftime(tsBuf, sizeof(tsBuf), "%Y-%m-%dT%H:%M:%SZ", &tm);

        // Summarize args with redaction
        std::ostringstream summary;
        summary << "{";
        if (args.is_object()) {
            bool first = true;
            for (const auto& [k, v] : args.get_object()) {
                if (!first) summary << ",";
                first = false;
                std::string lowerK = k;
                for (char& c : lowerK) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));

                if (lowerK == "message" || lowerK == "body" || lowerK == "base64" ||
                    lowerK == "media_path" || lowerK == "path" || lowerK == "query") {
                    std::string valStr = v.is_string() ? v.get<std::string>() : slimr::write_json(v);
                    summary << "\"" << k << "\":\"[redacted len=" << valStr.size() << "]\"";
                } else {
                    std::string valStr = v.is_string() ? v.get<std::string>() : slimr::write_json(v);
                    if (valStr.size() > 80) valStr = valStr.substr(0, 80) + "…";
                    summary << "\"" << k << "\":\"" << valStr << "\"";
                }
            }
        }
        summary << "}";

        std::string summaryStr = summary.str();
        if (summaryStr.size() > 200) {
            summaryStr = summaryStr.substr(0, 200) + "…";
        }

        slimr::Json record = slimr::Json::object_t{};
        record["ts"] = std::string(tsBuf);
        record["tool"] = tool;
        record["args_summary"] = summaryStr;

        std::ofstream out(logFile.string(), std::ios::app);
        if (out.is_open()) {
            out << slimr::write_json(record) << "\n";
            out.close();
            chmod(logFile.string().c_str(), 0600);
        }
    } catch (...) {
        // Audit log failures should not abort operations
    }
}

} // namespace whatsapp
