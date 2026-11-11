class WhatsappMcp < Formula
  desc "WhatsApp MCP Streamable HTTP server with OAuth 2.1 and browser QR pairing"
  homepage "https://github.com/bdombro/whatsapp-mcp"
  version "2.0.0"
  license "MIT"

  url "https://github.com/bdombro/whatsapp-mcp/releases/download/v2.0.0/whatsapp-mcp-v2.0.0-darwin-arm64.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"

  def install
    bin.install "whatsapp-mcp"
  end

  service do
    run [opt_bin/"whatsapp-mcp", "http", "--host", "127.0.0.1", "--port", "8088"]
    keep_alive true
    log_path var/"log/whatsapp-mcp.log"
    error_log_path var/"log/whatsapp-mcp.err.log"
  end

  def caveats
    <<~EOS
      whatsapp-mcp listens on http://127.0.0.1:8088/mcp with OAuth 2.1 + PKCE.
      Start the service with:
        brew services start bdombro/whatsapp-mcp/whatsapp-mcp

      It expects the sibling whatsapp-client sync daemon (~/.local/share/whatsapp-client/daemon.sock)
      for sending, downloading media, and browser QR pairing. Restarting whatsapp-mcp never restarts
      or duplicates whatsapp-client.
    EOS
  end

  test do
    env = {
      "BUGSINK_DSN" => "",
      "WHATSAPP_CLIENT_DATA_DIR" => testpath.to_s,
    }
    req = '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}' + "\n"
    out = with_env(env) { pipe_output("#{bin}/whatsapp-mcp mcp", req, 0) }
    assert_match "2026-07-28", out
  end
end
