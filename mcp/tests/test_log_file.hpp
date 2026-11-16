#pragma once

#include "log_file.hpp"
#include "test.hpp"

#include <chrono>
#include <filesystem>
#include <fstream>
#include <sstream>
#include <string>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>

namespace whatsapp::test {

class StdFdRestore {
  public:
    StdFdRestore()
        : savedStdout(::dup(STDOUT_FILENO)), savedStderr(::dup(STDERR_FILENO)) {}

    ~StdFdRestore() {
        (void)std::fflush(stdout);
        (void)std::fflush(stderr);
        if (savedStdout) {
            (void)::dup2(savedStdout.get(), STDOUT_FILENO);
        }
        if (savedStderr) {
            (void)::dup2(savedStderr.get(), STDERR_FILENO);
        }
    }

    StdFdRestore(const StdFdRestore &) = delete;
    StdFdRestore &operator=(const StdFdRestore &) = delete;

  private:
    app_log::UniqueFd savedStdout;
    app_log::UniqueFd savedStderr;
};

struct LogTempDir {
    std::filesystem::path path;
    LogTempDir() {
        static int counter = 0;
        ++counter;
        path = std::filesystem::temp_directory_path() /
               ("app-log-test-" + std::to_string(::getpid()) + "-" +
                std::to_string(counter));
        std::filesystem::create_directories(path);
    }
    ~LogTempDir() {
        std::error_code ec;
        std::filesystem::remove_all(path, ec);
    }
};

inline std::string readLogText(const std::filesystem::path &p) {
    std::ifstream in(p, std::ios::binary);
    std::ostringstream ss;
    ss << in.rdbuf();
    return ss.str();
}

TEST_CASE("log_file - redirects and rotates at tiny threshold") {
    LogTempDir tmp;
    const std::filesystem::path logPath = tmp.path / "nested" / "app.log";
    const std::filesystem::path backupPath = tmp.path / "nested" / "app.log.1";

    {
        StdFdRestore restore;
        auto opened = app_log::LogFileGuard::open(logPath, 128,
                                                  std::chrono::milliseconds(10));
        REQUIRE(opened.has_value());

        struct stat initSt{};
        REQUIRE_EQ(::stat(logPath.c_str(), &initSt), 0);
        CHECK_EQ(initSt.st_mode & 0777, 0600);

        const std::string firstBatch(150, 'A');
        (void)::write(STDOUT_FILENO, firstBatch.data(), firstBatch.size());

        bool rotatedFirst = false;
        for (int i = 0; i < 200; ++i) {
            if ((*opened)->rotations() >= 1 && std::filesystem::exists(backupPath)) {
                rotatedFirst = true;
                break;
            }
            std::this_thread::sleep_for(std::chrono::milliseconds(10));
        }
        REQUIRE(rotatedFirst);
        CHECK_EQ(readLogText(backupPath), firstBatch);

        struct stat rotSt{};
        REQUIRE_EQ(::stat(logPath.c_str(), &rotSt), 0);
        CHECK_EQ(rotSt.st_mode & 0777, 0600);

        const std::string secondBatch(150, 'B');
        (void)::write(STDERR_FILENO, secondBatch.data(), secondBatch.size());

        bool rotatedSecond = false;
        for (int i = 0; i < 200; ++i) {
            if ((*opened)->rotations() >= 2 &&
                readLogText(backupPath) == secondBatch) {
                rotatedSecond = true;
                break;
            }
            std::this_thread::sleep_for(std::chrono::milliseconds(10));
        }
        REQUIRE(rotatedSecond);
        CHECK(!std::filesystem::exists(tmp.path / "nested" / "app.log.2"));

        const std::string tail = "tail-line\n";
        (void)::write(STDOUT_FILENO, tail.data(), tail.size());
        CHECK_EQ(readLogText(logPath), tail);
    }
}

TEST_CASE("log_file - rotates existing oversized file on startup") {
    LogTempDir tmp;
    const std::filesystem::path logPath = tmp.path / "startup.log";
    const std::filesystem::path backupPath = tmp.path / "startup.log.1";

    {
        std::ofstream out(logPath, std::ios::binary);
        out << std::string(200, 'X');
    }

    {
        StdFdRestore restore;
        auto opened = app_log::LogFileGuard::open(logPath, 128,
                                                  std::chrono::milliseconds(50));
        REQUIRE(opened.has_value());
        CHECK(std::filesystem::exists(backupPath));
        CHECK_EQ(readLogText(backupPath), std::string(200, 'X'));

        const std::string fresh = "fresh\n";
        (void)::write(STDOUT_FILENO, fresh.data(), fresh.size());
        CHECK_EQ(readLogText(logPath), fresh);
    }
}

TEST_CASE("log_file - reports rename failure and keeps logging to main file") {
    if (::geteuid() == 0) {
        return;
    }
    LogTempDir tmp;
    const std::filesystem::path logDir = tmp.path / "ro_dir";
    const std::filesystem::path logPath = logDir / "app.log";
    const std::filesystem::path backupPath = logDir / "app.log.1";
    std::filesystem::create_directories(logDir);

    struct DirPermRestore {
        std::filesystem::path dir;
        ~DirPermRestore() { (void)::chmod(dir.c_str(), 0755); }
    } permRestore{logDir};

    {
        StdFdRestore restore;
        auto opened = app_log::LogFileGuard::open(logPath, 128,
                                                  std::chrono::milliseconds(10));
        REQUIRE(opened.has_value());

        REQUIRE_EQ(::chmod(logDir.c_str(), 0555), 0);
        const std::string batch(150, 'R');
        (void)::write(STDOUT_FILENO, batch.data(), batch.size());

        bool sawErr = false;
        for (int i = 0; i < 200; ++i) {
            if (readLogText(logPath).find("log rotation failed:") !=
                std::string::npos) {
                sawErr = true;
                break;
            }
            std::this_thread::sleep_for(std::chrono::milliseconds(10));
        }
        CHECK(sawErr);
        CHECK(!std::filesystem::exists(backupPath));
        CHECK_EQ((*opened)->rotations(), 0);

        const std::string tail = "still-writing\n";
        (void)::write(STDOUT_FILENO, tail.data(), tail.size());
        CHECK(readLogText(logPath).find(tail) != std::string::npos);
    }
}

TEST_CASE("log_file - rejects empty or unopenable path") {
    auto emptyRes = app_log::LogFileGuard::open("");
    CHECK(!emptyRes.has_value());

    LogTempDir tmp;
    auto dirRes = app_log::LogFileGuard::open(tmp.path);
    CHECK(!dirRes.has_value());
}

} // namespace whatsapp::test
