class WhatsappMcp < Formula
  desc "WhatsApp MCP Streamable HTTP server with OAuth 2.1 and browser QR pairing"
  homepage "https://github.com/bdombro/whatsapp-mcp"
  version "3.0.0"

  on_macos do
    url "https://github.com/bdombro/whatsapp-mcp/releases/download/v3.0.0/whatsapp-mcp-arm64-macos.tar.gz"
    sha256 "697b427e1f2de2774fd0371116b5f1527036f6e13e17fd357851211fd53aa9c9"
  end

  on_linux do
    url "https://github.com/bdombro/whatsapp-mcp/releases/download/v3.0.0/whatsapp-mcp-x64-linux.tar.gz"
    sha256 "bedc04906a2286256066356c6f6c2845f1082b57c0fb0de0eed2bb1777ea26be"
  end

  license "MIT"

  def install
    bin.install "whatsapp-mcp"
    chmod 0755, bin/"whatsapp-mcp"
    pkgshare.install "docs" if File.directory?("docs")
  end

  # Shares the whatsapp-client data dir (daemon.sock, whatsapp.db, messages.db)
  def post_install
    (var/"whatsapp-client").mkpath
    chmod 0700, var/"whatsapp-client"
    (var/"log").mkpath
  end

  service do
    run ["/bin/sh", "-c", "[ -f #{etc}/whatsapp-mcp/env ] && set -a && . #{etc}/whatsapp-mcp/env; exec #{opt_bin}/whatsapp-mcp http --log-file #{var}/log/whatsapp-mcp.log"]
    keep_alive true
    working_dir var/"whatsapp-client"
    log_path var/"log/whatsapp-mcp.log"
    error_log_path var/"log/whatsapp-mcp.log"
    environment_variables PATH: "#{HOMEBREW_PREFIX}/bin:/usr/bin:/bin"
  end

  def caveats
    <<~EOS
      whatsapp-mcp serves http://127.0.0.1:10008/mcp with OAuth 2.1 + PKCE.
      Start both services with:
        brew services start whatsapp-client
        brew services start whatsapp-mcp

      It expects the sibling whatsapp-client sync daemon (#{var}/whatsapp-client/daemon.sock)
      for sending, downloading media, and browser QR pairing. Restarting whatsapp-mcp never restarts
      or duplicates whatsapp-client.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/whatsapp-mcp --version")
    discover = '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}'
    env = { "BUGSINK_DSN" => "", "WHATSAPP_CLIENT_DATA_DIR" => testpath.to_s }
    out = with_env(env) { pipe_output("#{bin}/whatsapp-mcp mcp --skip-checks", "#{discover}\n") }
    assert_match "whatsapp-mcp", out
  end
end
