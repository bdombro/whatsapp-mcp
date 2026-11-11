cmake_minimum_required(VERSION 3.31)
if(CMAKE_HOST_APPLE AND NOT CMAKE_OSX_SYSROOT)
    execute_process(COMMAND xcrun --show-sdk-path
        OUTPUT_VARIABLE CMAKE_OSX_SYSROOT OUTPUT_STRIP_TRAILING_WHITESPACE
        COMMAND_ERROR_IS_FATAL ANY)
endif()
project(whatsapp LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 26)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)
include(CTest)
include(CheckIPOSupported)
include(CheckCXXSourceCompiles)
option(ENABLE_SANITIZERS "Enable address and undefined behavior sanitizers" OFF)

find_package(slimr-mcp CONFIG REQUIRED)
find_package(slimr-sqlite CONFIG REQUIRED)

add_library(app_options INTERFACE)
target_compile_options(app_options INTERFACE -ftrivial-auto-var-init=zero)
check_cxx_source_compiles("
#include <version>
#ifndef _LIBCPP_VERSION
#error libc++ is required for the selected hardening mode
#endif
#ifndef _LIBCPP_HARDENING_MODE_FAST
#error libc++ FAST hardening is unavailable
#endif
int main() { return 0; }" HAVE_LIBCPP_HARDENING)
if(NOT HAVE_LIBCPP_HARDENING)
    message(FATAL_ERROR "Use a Clang/libc++ toolchain supporting FAST hardening")
endif()
target_compile_definitions(app_options INTERFACE
    _LIBCPP_HARDENING_MODE=_LIBCPP_HARDENING_MODE_FAST)
if(ENABLE_SANITIZERS)
    target_compile_options(app_options INTERFACE
        -g -fsanitize=address,undefined -fno-sanitize-recover=undefined)
    target_link_options(app_options INTERFACE -fsanitize=address,undefined)
endif()
check_ipo_supported(RESULT HAVE_IPO OUTPUT IPO_ERROR)
if(NOT HAVE_IPO)
    message(FATAL_ERROR "Release IPO is required: ${IPO_ERROR}")
endif()

add_executable(whatsapp-mcp src/main.cpp)
target_include_directories(whatsapp-mcp PRIVATE src)
target_link_libraries(whatsapp-mcp PRIVATE slimr::mcp slimr::sqlite app_options)
set_target_properties(whatsapp-mcp PROPERTIES
    INTERPROCEDURAL_OPTIMIZATION_RELEASE ON
    RUNTIME_OUTPUT_DIRECTORY_RELEASE "${CMAKE_SOURCE_DIR}/build/bin")

if(BUILD_TESTING)
    find_package(slimr-test CONFIG REQUIRED)
    add_executable(whatsapp_test tests/test_main.cpp)
    target_include_directories(whatsapp_test PRIVATE src)
    target_link_libraries(whatsapp_test PRIVATE
        slimr::mcp slimr::sqlite slimr::test app_options)
    set_target_properties(whatsapp_test PROPERTIES INTERPROCEDURAL_OPTIMIZATION_RELEASE ON)
    add_test(NAME whatsapp COMMAND whatsapp_test)
    file(MAKE_DIRECTORY "${CMAKE_BINARY_DIR}/test-tmp" "${CMAKE_BINARY_DIR}/test-home")
    set_tests_properties(whatsapp PROPERTIES TIMEOUT 120
        ENVIRONMENT "BUGSINK_DSN=;TMPDIR=${CMAKE_BINARY_DIR}/test-tmp;HOME=${CMAKE_BINARY_DIR}/test-home;XDG_CONFIG_HOME=${CMAKE_BINARY_DIR}/test-home/config;XDG_DATA_HOME=${CMAKE_BINARY_DIR}/test-home/data;XDG_STATE_HOME=${CMAKE_BINARY_DIR}/test-home/state")
endif()
