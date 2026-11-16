#pragma once

#include <fcntl.h>
#include <spawn.h>
#include <sys/wait.h>
#include <unistd.h>

#include <cerrno>
#include <cstdlib>
#include <mutex>
#include <string>

extern char **environ;

namespace whatsapp {

namespace detail {

struct BrewFd {
    int fd = -1;
    BrewFd() = default;
    explicit BrewFd(int f) noexcept : fd(f) {}
    ~BrewFd() { reset(); }
    BrewFd(const BrewFd &) = delete;
    BrewFd &operator=(const BrewFd &) = delete;
    void reset(int next = -1) noexcept {
        if (fd >= 0) {
            ::close(fd);
        }
        fd = next;
    }
    [[nodiscard]] int get() const noexcept { return fd; }
};

struct BrewSpawnActions {
    posix_spawn_file_actions_t actions{};
    bool active = false;
    BrewSpawnActions() {
        if (posix_spawn_file_actions_init(&actions) == 0) {
            active = true;
        }
    }
    ~BrewSpawnActions() {
        if (active) {
            posix_spawn_file_actions_destroy(&actions);
        }
    }
    BrewSpawnActions(const BrewSpawnActions &) = delete;
    BrewSpawnActions &operator=(const BrewSpawnActions &) = delete;
};

struct BrewChildPid {
    pid_t pid = -1;
    bool reaped = false;
    BrewChildPid() = default;
    ~BrewChildPid() {
        if (pid > 0 && !reaped) {
            int status = 0;
            while (waitpid(pid, &status, 0) < 0 && errno == EINTR) {
            }
        }
    }
    BrewChildPid(const BrewChildPid &) = delete;
    BrewChildPid &operator=(const BrewChildPid &) = delete;
    bool waitSuccess() {
        if (pid <= 0) {
            return false;
        }
        int status = 0;
        pid_t w = -1;
        while ((w = waitpid(pid, &status, 0)) < 0 && errno == EINTR) {
        }
        reaped = true;
        return w == pid && WIFEXITED(status) && WEXITSTATUS(status) == 0;
    }
};

// Run `brew --prefix`; empty on any failure
inline std::string exec_brew_prefix() {
    int pipeRaw[2]{-1, -1};
    if (pipe(pipeRaw) != 0) {
        return "";
    }
    BrewFd readFd(pipeRaw[0]);
    BrewFd writeFd(pipeRaw[1]);
    if (fcntl(readFd.get(), F_SETFD, FD_CLOEXEC) != 0 ||
        fcntl(writeFd.get(), F_SETFD, FD_CLOEXEC) != 0) {
        return "";
    }

    BrewSpawnActions actions;
    if (!actions.active) {
        return "";
    }
    posix_spawn_file_actions_addopen(&actions.actions, STDIN_FILENO, "/dev/null", O_RDONLY, 0);
    posix_spawn_file_actions_addclose(&actions.actions, readFd.get());
    posix_spawn_file_actions_adddup2(&actions.actions, writeFd.get(), STDOUT_FILENO);
    posix_spawn_file_actions_addclose(&actions.actions, writeFd.get());

    char prog[] = "brew";
    char arg1[] = "--prefix";
    char *argv[] = {prog, arg1, nullptr};
    BrewChildPid child;
    const int rc = posix_spawnp(&child.pid, "brew", &actions.actions, nullptr, argv, environ);
    writeFd.reset();
    if (rc != 0 || child.pid <= 0) {
        child.pid = -1;
        return "";
    }

    std::string out;
    char buf[512];
    while (out.size() < 4096) {
        const ssize_t n = read(readFd.get(), buf, sizeof(buf));
        if (n < 0) {
            if (errno == EINTR)
                continue;
            break;
        }
        if (n == 0)
            break;
        out.append(buf, static_cast<size_t>(n));
    }

    if (!child.waitSuccess()) {
        return "";
    }
    while (!out.empty() && (out.back() == '\n' || out.back() == '\r' || out.back() == '/')) {
        out.pop_back();
    }
    return out;
}

} // namespace detail

// HOMEBREW_PREFIX, else `brew --prefix` (cached), else the platform default
inline std::string brew_prefix() {
    const char *env = std::getenv("HOMEBREW_PREFIX");
    if (env && *env) {
        std::string s(env);
        while (s.size() > 1 && s.back() == '/') {
            s.pop_back();
        }
        return s;
    }

    static std::once_flag once;
    static std::string cached;
    std::call_once(once, [] {
        cached = detail::exec_brew_prefix();
        if (cached.empty()) {
#if defined(__APPLE__) && defined(__aarch64__)
            cached = "/opt/homebrew";
#elif defined(__APPLE__)
            cached = "/usr/local";
#else
            cached = "/home/linuxbrew/.linuxbrew";
#endif
        }
    });
    return cached;
}

} // namespace whatsapp
