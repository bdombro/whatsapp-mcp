#!/usr/bin/env bash
set -euo pipefail

failures=0

fail() {
    echo "ERROR: $1" >&2
    echo "  Fix: $2" >&2
    failures=$((failures + 1))
}

for tool in "$@"; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        fail "Missing required tool on PATH: $tool" "brew install $tool"
    fi
done

llvm_bin="${LLVM_BIN:-}"
if [ -z "$llvm_bin" ] && command -v brew >/dev/null 2>&1; then
    if prefix="$(brew --prefix llvm@23 2>/dev/null)"; then
        llvm_bin="$prefix/bin"
    fi
fi

if [ -z "$llvm_bin" ] || [ ! -x "$llvm_bin/clang-format" ] || [ ! -x "$llvm_bin/clang-tidy" ]; then
    fail "Missing clang-format/clang-tidy in LLVM_BIN (${llvm_bin:-unset})" "brew install llvm@23"
fi

if [ -z "${VCPKG_ROOT:-}" ] || [ ! -x "$VCPKG_ROOT/vcpkg" ]; then
    fail "VCPKG_ROOT is not set or \$VCPKG_ROOT/vcpkg is missing" "git clone https://github.com/microsoft/vcpkg.git \"\$HOME/.local/share/vcpkg\" && \"\$HOME/.local/share/vcpkg/bootstrap-vcpkg.sh\" -disableMetrics && export VCPKG_ROOT=\"\$HOME/.local/share/vcpkg\""
fi

os="$(uname -s)"
compiler="${CXX:-}"
if [ -z "$compiler" ]; then
    if [ "$os" = "Darwin" ]; then
        compiler="/usr/bin/clang++"
    else
        compiler="${llvm_bin}/clang++"
    fi
fi

cxx_flags=(-std=c++26)
if [ "$os" = "Linux" ]; then
    cxx_flags+=(-stdlib=libc++)
fi

if ! command -v "$compiler" >/dev/null 2>&1 && [ ! -x "$compiler" ]; then
    fail "C++ compiler not found: $compiler" "Install Xcode Command Line Tools (macOS) or brew install llvm@23 (Linux)"
elif ! printf '#include <expected>\nint main() { return 0; }\n' | "$compiler" "${cxx_flags[@]}" -x c++ - -o /dev/null 2>/dev/null; then
    fail "Compiler $compiler failed C++26 check (${cxx_flags[*]})" "Upgrade Xcode Command Line Tools (macOS) or brew install llvm@23 (Linux)"
fi

if [ "$os" = "Linux" ]; then
    if ! command -v loginctl >/dev/null 2>&1; then
        fail "Missing loginctl for systemd user lingering check" "Install systemd or run on a systemd host"
    else
        linger="$(loginctl show-user "${USER:-$(id -un)}" -p Linger --value 2>/dev/null || true)"
        if [ "$linger" != "yes" ]; then
            fail "systemd user lingering is not enabled for ${USER:-$(id -un)}" "sudo loginctl enable-linger \"\$USER\""
        fi
    fi
fi

if [ "$failures" -ne 0 ]; then
    exit 1
fi
echo "doctor: all checks passed"
