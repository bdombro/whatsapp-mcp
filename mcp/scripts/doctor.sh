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
compiler="${CXX:-${llvm_bin}/clang++}"
cxx_flags=(-std=c++26 -stdlib=libc++)

if ! command -v "$compiler" >/dev/null 2>&1 && [ ! -x "$compiler" ]; then
    fail "C++ compiler not found: $compiler" "brew install llvm@23"
elif ! printf '#include <expected>\nint main() { return 0; }\n' | "$compiler" "${cxx_flags[@]}" -x c++ - -o /dev/null 2>/dev/null; then
    fail "Compiler $compiler failed C++26 check (${cxx_flags[*]})" "brew install llvm@23"
fi

if [ "$os" = "Darwin" ] && { command -v "$compiler" >/dev/null 2>&1 || [ -x "$compiler" ]; }; then
    libcxx_a="$("$compiler" -print-file-name=c++/libc++.a 2>/dev/null || true)"
    libcxx_dir="$(dirname "$libcxx_a")"
    if [ -z "$libcxx_a" ] || [ ! -f "$libcxx_a" ] || [ ! -f "$libcxx_dir/libc++abi.a" ]; then
        fail "Missing static libc++.a / libc++abi.a for $compiler" "brew install llvm@23"
    fi
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
