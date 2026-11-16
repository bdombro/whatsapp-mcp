#pragma once

#include <atomic>
#include <cerrno>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <expected>
#include <fcntl.h>
#include <filesystem>
#include <memory>
#include <mutex>
#include <string>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>
#include <utility>

namespace app_log {

inline constexpr std::uintmax_t DEFAULT_MAX_BYTES = 10ULL * 1024ULL * 1024ULL;
inline constexpr std::chrono::milliseconds DEFAULT_POLL_INTERVAL{5000};

class UniqueFd {
  public:
    UniqueFd() = default;
    explicit UniqueFd(int descriptor) noexcept : fdValue(descriptor) {}
    ~UniqueFd() { reset(); }

    UniqueFd(const UniqueFd &) = delete;
    UniqueFd &operator=(const UniqueFd &) = delete;

    UniqueFd(UniqueFd &&other) noexcept
        : fdValue(std::exchange(other.fdValue, -1)) {}

    UniqueFd &operator=(UniqueFd &&other) noexcept {
        if (this != &other) {
            reset();
            fdValue = std::exchange(other.fdValue, -1);
        }
        return *this;
    }

    void reset(int next = -1) noexcept {
        if (fdValue >= 0) {
            ::close(fdValue);
        }
        fdValue = next;
    }

    [[nodiscard]] int get() const noexcept { return fdValue; }
    explicit operator bool() const noexcept { return fdValue >= 0; }

  private:
    int fdValue = -1;
};

class LogFileGuard {
  private:
    struct PrivateTag {};

  public:
    LogFileGuard(PrivateTag, std::filesystem::path targetPath,
                 std::uintmax_t limitBytes,
                 std::chrono::milliseconds interval)
        : logPath(std::move(targetPath)), maxBytes(limitBytes),
          pollInterval(interval), watcher([this] { watchLoop(); }) {}

    ~LogFileGuard() {
        {
            std::lock_guard<std::mutex> lock(stopMutex);
            stopped = true;
        }
        stopCv.notify_all();
        if (watcher.joinable()) {
            watcher.join();
        }
    }

    LogFileGuard(const LogFileGuard &) = delete;
    LogFileGuard &operator=(const LogFileGuard &) = delete;
    LogFileGuard(LogFileGuard &&) = delete;
    LogFileGuard &operator=(LogFileGuard &&) = delete;

    static std::expected<std::unique_ptr<LogFileGuard>, std::string>
    open(std::filesystem::path targetPath,
         std::uintmax_t limitBytes = DEFAULT_MAX_BYTES,
         std::chrono::milliseconds interval = DEFAULT_POLL_INTERVAL) {
        if (targetPath.empty()) {
            return std::unexpected("Log file path must not be empty");
        }

        const std::filesystem::path parent = targetPath.parent_path();
        if (!parent.empty()) {
            std::error_code ec;
            std::filesystem::create_directories(parent, ec);
            if (ec) {
                return std::unexpected("Failed to create log directory " +
                                       parent.string() + ": " + ec.message());
            }
        }

        struct stat st{};
        if (::stat(targetPath.c_str(), &st) == 0 && S_ISREG(st.st_mode) &&
            st.st_size >= 0 &&
            static_cast<std::uintmax_t>(st.st_size) > limitBytes) {
            const std::string backup = targetPath.string() + ".1";
            if (::rename(targetPath.c_str(), backup.c_str()) != 0) {
                return std::unexpected("Failed to rotate existing log file " +
                                       targetPath.string() + ": " +
                                       std::strerror(errno));
            }
        }

        UniqueFd fd(::open(targetPath.c_str(),
                           O_WRONLY | O_CREAT | O_APPEND | O_CLOEXEC, 0600));
        if (!fd) {
            return std::unexpected("Failed to open log file " +
                                   targetPath.string() + ": " +
                                   std::strerror(errno));
        }

        (void)std::fflush(stdout);
        (void)std::fflush(stderr);
        if (::dup2(fd.get(), STDOUT_FILENO) < 0 ||
            ::dup2(fd.get(), STDERR_FILENO) < 0) {
            return std::unexpected("Failed to redirect stdout/stderr to " +
                                   targetPath.string() + ": " +
                                   std::strerror(errno));
        }

        return std::make_unique<LogFileGuard>(
            PrivateTag{}, std::move(targetPath), limitBytes, interval);
    }

    [[nodiscard]] std::uint64_t rotations() const noexcept {
        return rotationCount.load();
    }

  private:
    static void writeStderrLine(const std::string &line) noexcept {
        size_t total = 0;
        while (total < line.size()) {
            const ssize_t n =
                ::write(STDERR_FILENO, line.data() + total, line.size() - total);
            if (n < 0) {
                if (errno == EINTR) {
                    continue;
                }
                break;
            }
            if (n == 0) {
                break;
            }
            total += static_cast<size_t>(n);
        }
    }

    void watchLoop() {
        std::unique_lock<std::mutex> lock(stopMutex);
        while (!stopped) {
            stopCv.wait_for(lock, pollInterval, [this] { return stopped; });
            if (stopped) {
                break;
            }
            lock.unlock();
            rotateIfNeeded();
            lock.lock();
        }
    }

    void rotateIfNeeded() {
        struct stat st{};
        if (::stat(logPath.c_str(), &st) != 0 || !S_ISREG(st.st_mode) ||
            st.st_size < 0 ||
            static_cast<std::uintmax_t>(st.st_size) <= maxBytes) {
            return;
        }

        (void)std::fflush(stdout);
        (void)std::fflush(stderr);
        const std::string backup = logPath.string() + ".1";
        if (::rename(logPath.c_str(), backup.c_str()) != 0) {
            writeStderrLine("log rotation failed: rename: " +
                            std::string(std::strerror(errno)) + "\n");
            return;
        }
        UniqueFd nextFd(::open(logPath.c_str(),
                               O_WRONLY | O_CREAT | O_APPEND | O_CLOEXEC,
                               0600));
        if (!nextFd) {
            const int openErr = errno;
            std::string msg =
                "log rotation failed: reopen: " + std::string(std::strerror(openErr));
            if (::rename(backup.c_str(), logPath.c_str()) != 0) {
                msg += " (rollback rename failed: " +
                       std::string(std::strerror(errno)) + ")";
            }
            msg.push_back('\n');
            writeStderrLine(msg);
            return;
        }
        if (::dup2(nextFd.get(), STDOUT_FILENO) < 0 ||
            ::dup2(nextFd.get(), STDERR_FILENO) < 0) {
            writeStderrLine("log rotation failed: dup2: " +
                            std::string(std::strerror(errno)) + "\n");
            return;
        }
        rotationCount.fetch_add(1);
    }

    std::filesystem::path logPath;
    std::uintmax_t maxBytes = DEFAULT_MAX_BYTES;
    std::chrono::milliseconds pollInterval = DEFAULT_POLL_INTERVAL;
    std::atomic<std::uint64_t> rotationCount{0};
    std::mutex stopMutex;
    std::condition_variable stopCv;
    bool stopped = false;
    std::thread watcher;
};

} // namespace app_log
