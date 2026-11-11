#pragma once

#include <cctype>
#include <cstdio>
#include <ctime>
#include <optional>
#include <stdexcept>
#include <string>
#include <string_view>

namespace whatsapp {

/// Normalizes mixed-offset timestamp strings to "YYYY-MM-DD HH:MM:SS" UTC.
/// Supports Go time.String() ("2026-11-08 00:40:38 +0100 IST"), RFC3339, SQLite datetime, and YYYY-MM-DD.
inline std::optional<std::string> wa_utc_normalize(std::string_view sv) {
    while (!sv.empty() && std::isspace(static_cast<unsigned char>(sv.front()))) sv.remove_prefix(1);
    while (!sv.empty() && std::isspace(static_cast<unsigned char>(sv.back()))) sv.remove_suffix(1);
    if (sv.size() < 10) return std::nullopt;

    int year = 0, month = 0, day = 0;
    int hour = 0, min = 0, sec = 0;

    // Parse YYYY-MM-DD
    if (std::sscanf(sv.data(), "%4d-%2d-%2d", &year, &month, &day) != 3) {
        return std::nullopt;
    }
    if (month < 1 || month > 12 || day < 1 || day > 31) {
        return std::nullopt;
    }

    sv.remove_prefix(10);
    int offset_seconds = 0;

    if (!sv.empty() && (sv.front() == ' ' || sv.front() == 'T')) {
        sv.remove_prefix(1);
        if (sv.size() >= 8 && sv[2] == ':' && sv[5] == ':') {
            if (std::sscanf(sv.data(), "%2d:%2d:%2d", &hour, &min, &sec) == 3) {
                sv.remove_prefix(8);
                // Skip fractional seconds (.123456789)
                if (!sv.empty() && sv.front() == '.') {
                    sv.remove_prefix(1);
                    while (!sv.empty() && std::isdigit(static_cast<unsigned char>(sv.front()))) {
                        sv.remove_prefix(1);
                    }
                }
                while (!sv.empty() && std::isspace(static_cast<unsigned char>(sv.front()))) {
                    sv.remove_prefix(1);
                }
                // Parse timezone / offset
                if (!sv.empty()) {
                    if (sv.front() == 'Z' || sv.front() == 'z') {
                        offset_seconds = 0;
                    } else if (sv.front() == '+' || sv.front() == '-') {
                        char sign = sv.front();
                        sv.remove_prefix(1);
                        int off_h = 0, off_m = 0;
                        if (sv.size() >= 5 && sv[2] == ':') {
                            std::sscanf(sv.data(), "%2d:%2d", &off_h, &off_m);
                        } else if (sv.size() >= 4) {
                            std::sscanf(sv.data(), "%2d%2d", &off_h, &off_m);
                        } else if (sv.size() >= 2) {
                            std::sscanf(sv.data(), "%2d", &off_h);
                        }
                        offset_seconds = (off_h * 3600 + off_m * 60) * (sign == '-' ? -1 : 1);
                    }
                }
            }
        }
    }

    struct tm tm{};
    tm.tm_year = year - 1900;
    tm.tm_mon = month - 1;
    tm.tm_mday = day;
    tm.tm_hour = hour;
    tm.tm_min = min;
    tm.tm_sec = sec;
    tm.tm_isdst = 0;

    time_t t = timegm(&tm);
    if (t == static_cast<time_t>(-1) && (year != 1969 && year != 1970)) {
        return std::nullopt;
    }
    t -= offset_seconds;

    struct tm res_tm{};
    gmtime_r(&t, &res_tm);

    char buf[32];
    std::snprintf(buf, sizeof(buf), "%04d-%02d-%02d %02d:%02d:%02d",
                  res_tm.tm_year + 1900, res_tm.tm_mon + 1, res_tm.tm_mday,
                  res_tm.tm_hour, res_tm.tm_min, res_tm.tm_sec);
    return std::string(buf);
}

/// Parses date bounds for export: accepts YYYY-MM-DD (local time) or RFC3339.
/// For until with YYYY-MM-DD (end=true), includes the whole day by adding 1 day.
inline std::string parse_date_bound(std::string_view s, bool end) {
    while (!s.empty() && std::isspace(static_cast<unsigned char>(s.front()))) s.remove_prefix(1);
    while (!s.empty() && std::isspace(static_cast<unsigned char>(s.back()))) s.remove_suffix(1);
    if (s.empty()) return "";

    // Check for bare YYYY-MM-DD
    if (s.size() == 10 && s[4] == '-' && s[7] == '-') {
        int y = 0, m = 0, d = 0;
        if (std::sscanf(s.data(), "%4d-%2d-%2d", &y, &m, &d) == 3 && m >= 1 && m <= 12 && d >= 1 && d <= 31) {
            struct tm tm{};
            tm.tm_year = y - 1900;
            tm.tm_mon = m - 1;
            tm.tm_mday = d + (end ? 1 : 0);
            tm.tm_isdst = -1;
            time_t t = mktime(&tm);
            if (t != static_cast<time_t>(-1)) {
                struct tm utc{};
                gmtime_r(&t, &utc);
                char buf[32];
                std::snprintf(buf, sizeof(buf), "%04d-%02d-%02d %02d:%02d:%02d",
                              utc.tm_year + 1900, utc.tm_mon + 1, utc.tm_mday,
                              utc.tm_hour, utc.tm_min, utc.tm_sec);
                return std::string(buf);
            }
        }
    }

    // Try wa_utc_normalize for RFC3339 or full timestamps
    auto norm = wa_utc_normalize(s);
    if (norm) {
        return *norm;
    }

    throw std::runtime_error("invalid date \"" + std::string(s) + "\" (use YYYY-MM-DD or RFC3339)");
}

/// Converts UTC "YYYY-MM-DD HH:MM:SS" into local "YYYY-MM-DD HH:MM".
inline std::string utc_to_local_minute(std::string_view utcStr) {
    int y = 0, m = 0, d = 0, h = 0, min = 0, s = 0;
    if (std::sscanf(utcStr.data(), "%4d-%2d-%2d %2d:%2d:%2d", &y, &m, &d, &h, &min, &s) >= 5) {
        struct tm tm{};
        tm.tm_year = y - 1900;
        tm.tm_mon = m - 1;
        tm.tm_mday = d;
        tm.tm_hour = h;
        tm.tm_min = min;
        tm.tm_sec = s;
        time_t t = timegm(&tm);
        if (t != static_cast<time_t>(-1)) {
            struct tm local{};
            localtime_r(&t, &local);
            char buf[32];
            std::snprintf(buf, sizeof(buf), "%04d-%02d-%02d %02d:%02d",
                          local.tm_year + 1900, local.tm_mon + 1, local.tm_mday,
                          local.tm_hour, local.tm_min);
            return std::string(buf);
        }
    }
    if (utcStr.size() >= 16) {
        return std::string(utcStr.substr(0, 16));
    }
    return std::string(utcStr);
}

} // namespace whatsapp
