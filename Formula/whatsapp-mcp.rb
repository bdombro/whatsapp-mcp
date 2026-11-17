class WhatsappMcp < Formula
  desc "WhatsApp MCP Streamable HTTP server with OAuth 2.1 and browser QR pairing"
  homepage "https://github.com/bdombro/whatsapp-mcp"
  version "3.1.2"

  on_macos do
    url "https://github.com/bdombro/whatsapp-mcp/releases/download/v3.1.2/whatsapp-mcp-arm64-macos.tar.gz"
    sha256 "22237b0fe917cda3616e012adf76019e0b56529f6228f3efc68862304a2ff1e7"
  end

  on_linux do
    url "https://github.com/bdombro/whatsapp-mcp/releases/download/v3.1.2/whatsapp-mcp-x64-linux.tar.gz"
    sha256 "95947d013ec2390afe25e198c1cbb1f6face377498df295eb561d101bc4c1fbf"
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
    require "net/http"
    assert_match version.to_s, shell_output("#{bin}/whatsapp-mcp --version")
    env = { "BUGSINK_DSN" => "", "WHATSAPP_CLIENT_DATA_DIR" => testpath.to_s }
    r, w = IO.pipe
    pid = spawn(env, "#{bin}/whatsapp-mcp", "--skip-checks", "http", "--host", "127.0.0.1", "--port", "0", err: w)
    w.close
    begin
      port = nil
      r.each_line { |line| break if (port = line[%r{listening on http://[\d.]+:(\d+)/mcp}, 1]) }
      assert port, "server did not report a listening port"

      prm = Net::HTTP.get_response(URI("http://127.0.0.1:#{port}/.well-known/oauth-protected-resource"))
      assert_equal "200", prm.code
      assert_match "http://127.0.0.1:#{port}/mcp", prm.body

      discover = '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}'
      res = Net::HTTP.post(URI("http://127.0.0.1:#{port}/mcp"), discover,
                           "Content-Type"         => "application/json",
                           "Accept"               => "application/json, text/event-stream",
                           "MCP-Protocol-Version" => "2026-07-28",
                           "Mcp-Method"           => "server/discover")
      assert_equal "401", res.code
      assert_match "resource_metadata=", res["www-authenticate"].to_s
    ensure
      Process.kill("TERM", pid)
      Process.wait(pid)
    end
  end
end
