#pragma once

#include <algorithm>
#include <array>
#include <cstdint>
#include <sstream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace whatsapp {

namespace qr_detail {

struct EcLSpec {
    int ecPerBlock;
    int g1Blocks;
    int g1Data;
    int g2Blocks;
    int g2Data;
};

// ISO/IEC 18004 Table 9, Error Correction Level L for Versions 1..20.
inline constexpr std::array<EcLSpec, 20> kEcLSpecs = {{
    {7, 1, 19, 0, 0},     // V1
    {10, 1, 34, 0, 0},    // V2
    {15, 1, 55, 0, 0},    // V3
    {20, 1, 80, 0, 0},    // V4
    {26, 1, 108, 0, 0},   // V5
    {18, 2, 68, 0, 0},    // V6
    {20, 2, 78, 0, 0},    // V7
    {24, 2, 97, 0, 0},    // V8
    {30, 2, 116, 0, 0},   // V9
    {18, 2, 68, 2, 69},   // V10
    {20, 4, 81, 0, 0},    // V11
    {24, 2, 92, 2, 93},   // V12
    {26, 4, 107, 0, 0},   // V13
    {30, 3, 115, 1, 116}, // V14
    {22, 5, 87, 1, 88},   // V15
    {24, 5, 98, 1, 99},   // V16
    {28, 1, 107, 5, 108}, // V17
    {30, 5, 120, 1, 121}, // V18
    {28, 3, 113, 4, 114}, // V19
    {28, 3, 107, 5, 108}, // V20
}};

inline std::vector<int> alignment_positions(int version) {
    if (version == 1) return {};
    int numAlign = version / 7 + 2;
    int step = (version == 32) ? 26 : ((version * 4 + numAlign * 2 + 1) / (numAlign * 2 - 2) * 2);
    std::vector<int> result;
    result.reserve(static_cast<size_t>(numAlign));
    result.push_back(6);
    int pos = version * 4 + 10;
    for (int i = 1; i < numAlign; ++i) {
        result.push_back(pos);
        pos -= step;
    }
    std::sort(result.begin(), result.end());
    return result;
}

inline uint8_t gf_mul(uint8_t x, uint8_t y) noexcept {
    int z = 0;
    for (int i = 7; i >= 0; --i) {
        z = (z << 1) ^ ((z >> 7) * 0x11D);
        z ^= ((y >> i) & 1) * x;
    }
    return static_cast<uint8_t>(z);
}

inline std::vector<uint8_t> rs_divisor(int degree) {
    std::vector<uint8_t> result(static_cast<size_t>(degree), 0);
    result.back() = 1;
    uint8_t root = 1;
    for (int i = 0; i < degree; ++i) {
        for (size_t j = 0; j < result.size(); ++j) {
            result[j] = gf_mul(result[j], root);
            if (j + 1 < result.size()) {
                result[j] ^= result[j + 1];
            }
        }
        root = gf_mul(root, 0x02);
    }
    return result;
}

inline std::vector<uint8_t> rs_remainder(const std::vector<uint8_t>& data, const std::vector<uint8_t>& divisor) {
    std::vector<uint8_t> result(divisor.size(), 0);
    for (uint8_t b : data) {
        uint8_t factor = b ^ result.front();
        result.erase(result.begin());
        result.push_back(0);
        for (size_t i = 0; i < divisor.size(); ++i) {
            result[i] ^= gf_mul(divisor[i], factor);
        }
    }
    return result;
}

inline void append_bits(std::vector<uint8_t>& out, uint32_t val, int len) {
    for (int i = len - 1; i >= 0; --i) {
        out.push_back(static_cast<uint8_t>((val >> i) & 1U));
    }
}

class QrMatrix {
public:
    explicit QrMatrix(int version)
        : version_(version),
          size_(version * 4 + 17),
          modules_(static_cast<size_t>(size_ * size_), false),
          isFunc_(static_cast<size_t>(size_ * size_), false) {
        drawFunctionPatterns();
    }

    int size() const noexcept { return size_; }

    bool get(int x, int y) const noexcept {
        return modules_[static_cast<size_t>(y * size_ + x)];
    }

    void encodeBytePayload(std::string_view payload) {
        const auto& spec = kEcLSpecs[static_cast<size_t>(version_ - 1)];
        const int totalDataBytes = spec.g1Blocks * spec.g1Data + spec.g2Blocks * spec.g2Data;

        std::vector<uint8_t> bits;
        bits.reserve(static_cast<size_t>(totalDataBytes * 8));
        // Byte mode indicator = 0100
        append_bits(bits, 0x4, 4);
        int ccBits = (version_ <= 9) ? 8 : 16;
        append_bits(bits, static_cast<uint32_t>(payload.size()), ccBits);
        for (unsigned char ch : payload) {
            append_bits(bits, ch, 8);
        }

        const size_t capacityBits = static_cast<size_t>(totalDataBytes) * 8;
        const size_t termBits = std::min<size_t>(4, capacityBits - bits.size());
        append_bits(bits, 0, static_cast<int>(termBits));
        while (bits.size() % 8 != 0) {
            bits.push_back(0);
        }
        for (uint8_t padByte = 0xEC; bits.size() < capacityBits; padByte ^= 0xEC ^ 0x11) {
            append_bits(bits, padByte, 8);
        }

        std::vector<uint8_t> dataCodewords(static_cast<size_t>(totalDataBytes), 0);
        for (size_t i = 0; i < bits.size(); ++i) {
            dataCodewords[i / 8] = static_cast<uint8_t>((dataCodewords[i / 8] << 1) | bits[i]);
        }

        auto divisor = rs_divisor(spec.ecPerBlock);
        const int totalBlocks = spec.g1Blocks + spec.g2Blocks;
        std::vector<std::vector<uint8_t>> blocks;
        blocks.reserve(static_cast<size_t>(totalBlocks));
        size_t offset = 0;
        const int shortLen = spec.g1Data;
        const int longLen = (spec.g2Blocks > 0) ? spec.g2Data : spec.g1Data;
        for (int i = 0; i < totalBlocks; ++i) {
            int datLen = (i < spec.g1Blocks) ? spec.g1Data : spec.g2Data;
            std::vector<uint8_t> dat(
                dataCodewords.begin() + static_cast<ptrdiff_t>(offset),
                dataCodewords.begin() + static_cast<ptrdiff_t>(offset + static_cast<size_t>(datLen)));
            offset += static_cast<size_t>(datLen);
            auto ecc = rs_remainder(dat, divisor);
            if (datLen < longLen) {
                dat.push_back(0); // sentinel for interleaving alignment
            }
            dat.insert(dat.end(), ecc.begin(), ecc.end());
            blocks.push_back(std::move(dat));
        }

        std::vector<uint8_t> finalCodewords;
        finalCodewords.reserve(static_cast<size_t>(totalDataBytes + totalBlocks * spec.ecPerBlock));
        for (size_t i = 0; i < static_cast<size_t>(longLen + spec.ecPerBlock); ++i) {
            for (int b = 0; b < totalBlocks; ++b) {
                if (i == static_cast<size_t>(shortLen) && b < spec.g1Blocks && spec.g2Blocks > 0) {
                    continue;
                }
                finalCodewords.push_back(blocks[static_cast<size_t>(b)][i]);
            }
        }

        drawCodewords(finalCodewords);

        int bestMask = 0;
        long long minPenalty = std::numeric_limits<long long>::max();
        for (int mask = 0; mask < 8; ++mask) {
            applyMask(mask);
            drawFormatBits(mask);
            long long p = penaltyScore();
            if (p < minPenalty) {
                minPenalty = p;
                bestMask = mask;
            }
            applyMask(mask); // XOR toggles back
        }
        applyMask(bestMask);
        drawFormatBits(bestMask);
    }

private:
    void setFunctionModule(int x, int y, bool isDark) {
        size_t idx = static_cast<size_t>(y * size_ + x);
        modules_[idx] = isDark;
        isFunc_[idx] = true;
    }

    void drawFinderPattern(int cx, int cy) {
        for (int dy = -4; dy <= 4; ++dy) {
            for (int dx = -4; dx <= 4; ++dx) {
                int x = cx + dx;
                int y = cy + dy;
                if (x >= 0 && x < size_ && y >= 0 && y < size_) {
                    int dist = std::max(std::abs(dx), std::abs(dy));
                    setFunctionModule(x, y, dist != 2 && dist != 4);
                }
            }
        }
    }

    void drawAlignmentPattern(int cx, int cy) {
        for (int dy = -2; dy <= 2; ++dy) {
            for (int dx = -2; dx <= 2; ++dx) {
                setFunctionModule(cx + dx, cy + dy, std::max(std::abs(dx), std::abs(dy)) != 1);
            }
        }
    }

    void drawFormatBits(int mask) {
        // EC Level L = 01
        int data = (1 << 3) | mask;
        int rem = data;
        for (int i = 0; i < 10; ++i) {
            rem = (rem << 1) ^ (((rem >> 9) & 1) * 0x537);
        }
        int bits = ((data << 10) | rem) ^ 0x5412;

        for (int i = 0; i <= 5; ++i) setFunctionModule(8, i, ((bits >> i) & 1) != 0);
        setFunctionModule(8, 7, ((bits >> 6) & 1) != 0);
        setFunctionModule(8, 8, ((bits >> 7) & 1) != 0);
        setFunctionModule(7, 8, ((bits >> 8) & 1) != 0);
        for (int i = 9; i < 15; ++i) setFunctionModule(14 - i, 8, ((bits >> i) & 1) != 0);

        for (int i = 0; i < 8; ++i) setFunctionModule(size_ - 1 - i, 8, ((bits >> i) & 1) != 0);
        for (int i = 8; i < 15; ++i) setFunctionModule(8, size_ - 15 + i, ((bits >> i) & 1) != 0);
        setFunctionModule(8, size_ - 8, true);
    }

    void drawVersion() {
        if (version_ < 7) return;
        int rem = version_;
        for (int i = 0; i < 12; ++i) {
            rem = (rem << 1) ^ (((rem >> 11) & 1) * 0x1F25);
        }
        int bits = (version_ << 12) | rem;
        for (int i = 0; i < 18; ++i) {
            bool bit = ((bits >> i) & 1) != 0;
            int a = size_ - 11 + (i % 3);
            int b = i / 3;
            setFunctionModule(a, b, bit);
            setFunctionModule(b, a, bit);
        }
    }

    void drawFunctionPatterns() {
        for (int i = 0; i < size_; ++i) {
            setFunctionModule(6, i, i % 2 == 0);
            setFunctionModule(i, 6, i % 2 == 0);
        }
        drawFinderPattern(3, 3);
        drawFinderPattern(size_ - 4, 3);
        drawFinderPattern(3, size_ - 4);

        auto pos = alignment_positions(version_);
        size_t num = pos.size();
        for (size_t i = 0; i < num; ++i) {
            for (size_t j = 0; j < num; ++j) {
                if ((i == 0 && j == 0) || (i == 0 && j == num - 1) || (i == num - 1 && j == 0)) {
                    continue;
                }
                drawAlignmentPattern(pos[i], pos[j]);
            }
        }
        drawFormatBits(0);
        drawVersion();
    }

    void drawCodewords(const std::vector<uint8_t>& data) {
        size_t i = 0;
        const size_t totalBits = data.size() * 8;
        for (int right = size_ - 1; right >= 1; right -= 2) {
            if (right == 6) right = 5;
            for (int vert = 0; vert < size_; ++vert) {
                for (int j = 0; j < 2; ++j) {
                    int x = right - j;
                    bool upward = ((right + 1) & 2) == 0;
                    int y = upward ? (size_ - 1 - vert) : vert;
                    size_t idx = static_cast<size_t>(y * size_ + x);
                    if (!isFunc_[idx] && i < totalBits) {
                        bool dark = ((data[i >> 3] >> (7 - (i & 7))) & 1) != 0;
                        modules_[idx] = dark;
                        ++i;
                    }
                }
            }
        }
    }

    void applyMask(int mask) {
        for (int y = 0; y < size_; ++y) {
            for (int x = 0; x < size_; ++x) {
                size_t idx = static_cast<size_t>(y * size_ + x);
                if (isFunc_[idx]) continue;
                bool invert = false;
                switch (mask) {
                    case 0: invert = (x + y) % 2 == 0; break;
                    case 1: invert = y % 2 == 0; break;
                    case 2: invert = x % 3 == 0; break;
                    case 3: invert = (x + y) % 3 == 0; break;
                    case 4: invert = (x / 3 + y / 2) % 2 == 0; break;
                    case 5: invert = (x * y) % 2 + (x * y) % 3 == 0; break;
                    case 6: invert = ((x * y) % 2 + (x * y) % 3) % 2 == 0; break;
                    case 7: invert = ((x + y) % 2 + (x * y) % 3) % 2 == 0; break;
                    default: break;
                }
                if (invert) {
                    modules_[idx] = !modules_[idx];
                }
            }
        }
    }

    long long penaltyScore() const {
        long long result = 0;
        // Rule 1: runs of 5+ same color in rows and columns
        for (int y = 0; y < size_; ++y) {
            bool runColor = false;
            int runLen = 0;
            for (int x = 0; x < size_; ++x) {
                bool c = get(x, y);
                if (x == 0 || c != runColor) {
                    if (runLen >= 5) result += 3 + (runLen - 5);
                    runColor = c;
                    runLen = 1;
                } else {
                    ++runLen;
                }
            }
            if (runLen >= 5) result += 3 + (runLen - 5);
        }
        for (int x = 0; x < size_; ++x) {
            bool runColor = false;
            int runLen = 0;
            for (int y = 0; y < size_; ++y) {
                bool c = get(x, y);
                if (y == 0 || c != runColor) {
                    if (runLen >= 5) result += 3 + (runLen - 5);
                    runColor = c;
                    runLen = 1;
                } else {
                    ++runLen;
                }
            }
            if (runLen >= 5) result += 3 + (runLen - 5);
        }
        // Rule 2: 2x2 blocks
        for (int y = 0; y < size_ - 1; ++y) {
            for (int x = 0; x < size_ - 1; ++x) {
                bool c = get(x, y);
                if (c == get(x + 1, y) && c == get(x, y + 1) && c == get(x + 1, y + 1)) {
                    result += 3;
                }
            }
        }
        // Rule 3: finder-like 1:1:3:1:1 patterns
        auto hasFinderLike = [](const std::vector<bool>& line) -> long long {
            long long hits = 0;
            const int n = static_cast<int>(line.size());
            for (int i = 0; i + 11 <= n; ++i) {
                bool p1 = line[static_cast<size_t>(i)] && !line[static_cast<size_t>(i + 1)] &&
                          line[static_cast<size_t>(i + 2)] && line[static_cast<size_t>(i + 3)] &&
                          line[static_cast<size_t>(i + 4)] && !line[static_cast<size_t>(i + 5)] &&
                          line[static_cast<size_t>(i + 6)] && !line[static_cast<size_t>(i + 7)] &&
                          !line[static_cast<size_t>(i + 8)] && !line[static_cast<size_t>(i + 9)] &&
                          !line[static_cast<size_t>(i + 10)];
                bool p2 = !line[static_cast<size_t>(i)] && !line[static_cast<size_t>(i + 1)] &&
                          !line[static_cast<size_t>(i + 2)] && !line[static_cast<size_t>(i + 3)] &&
                          line[static_cast<size_t>(i + 4)] && !line[static_cast<size_t>(i + 5)] &&
                          line[static_cast<size_t>(i + 6)] && line[static_cast<size_t>(i + 7)] &&
                          line[static_cast<size_t>(i + 8)] && !line[static_cast<size_t>(i + 9)] &&
                          line[static_cast<size_t>(i + 10)];
                if (p1 || p2) hits += 40;
            }
            return hits;
        };
        std::vector<bool> line(static_cast<size_t>(size_));
        for (int y = 0; y < size_; ++y) {
            for (int x = 0; x < size_; ++x) line[static_cast<size_t>(x)] = get(x, y);
            result += hasFinderLike(line);
        }
        for (int x = 0; x < size_; ++x) {
            for (int y = 0; y < size_; ++y) line[static_cast<size_t>(y)] = get(x, y);
            result += hasFinderLike(line);
        }
        // Rule 4: dark proportion
        long long dark = 0;
        for (bool m : modules_) {
            if (m) ++dark;
        }
        long long total = static_cast<long long>(size_) * size_;
        long long k = std::abs(dark * 20 - total * 10) / total;
        result += k * 10;
        return result;
    }

    int version_;
    int size_;
    std::vector<bool> modules_;
    std::vector<bool> isFunc_;
};

inline int select_qr_version(size_t byteLen) {
    for (int v = 1; v <= 20; ++v) {
        const auto& spec = kEcLSpecs[static_cast<size_t>(v - 1)];
        const int dataBytes = spec.g1Blocks * spec.g1Data + spec.g2Blocks * spec.g2Data;
        const int headerBits = 4 + ((v <= 9) ? 8 : 16);
        const size_t availBytes = (static_cast<size_t>(dataBytes) * 8 - static_cast<size_t>(headerBits)) / 8;
        if (byteLen <= availBytes) {
            return v;
        }
    }
    throw std::runtime_error("QR payload exceeds Version 20 EC-L capacity");
}

} // namespace qr_detail

inline qr_detail::QrMatrix encode_qr_matrix(std::string_view payload) {
    int version = qr_detail::select_qr_version(payload.size());
    qr_detail::QrMatrix matrix(version);
    matrix.encodeBytePayload(payload);
    return matrix;
}

inline std::string qr_to_svg(std::string_view payload, int quietZone = 4) {
    auto matrix = encode_qr_matrix(payload);
    const int n = matrix.size();
    const int total = n + quietZone * 2;

    std::ostringstream svg;
    svg << "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 "
        << total << " " << total << "\" shape-rendering=\"crispEdges\">";
    svg << "<rect width=\"100%\" height=\"100%\" fill=\"#ffffff\"/>";
    svg << "<path d=\"";
    for (int y = 0; y < n; ++y) {
        for (int x = 0; x < n; ++x) {
            if (matrix.get(x, y)) {
                svg << "M" << (x + quietZone) << "," << (y + quietZone) << "h1v1h-1z";
            }
        }
    }
    svg << "\" fill=\"#000000\"/></svg>";
    return svg.str();
}

} // namespace whatsapp
