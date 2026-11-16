class WhatsappClient < Formula
  desc "WhatsApp sync daemon and CLI backed by whatsmeow"
  homepage "https://github.com/bdombro/whatsapp-mcp"
  version "3.0.0"

  on_macos do
    url "https://github.com/bdombro/whatsapp-mcp/releases/download/v3.0.0/whatsapp-client-arm64-macos.tar.gz"
    sha256 "0031c0d253c795ef08daf9db24016664f7e277413442c45c09b241e0b92bbb70"
  end

  on_linux do
    url "https://github.com/bdombro/whatsapp-mcp/releases/download/v3.0.0/whatsapp-client-x64-linux.tar.gz"
    sha256 "693c340547130a5d56ea2050956d6e51530ea60726032f3a3694df7d0af6c407"
  end

  license "MIT"

  def install
    bin.install "whatsapp-client"
    chmod 0755, bin/"whatsapp-client"
  end

  def post_install
    (var/"whatsapp-client").mkpath
    chmod 0700, var/"whatsapp-client"
    (var/"log").mkpath
  end

  service do
    run ["/bin/sh", "-c", "[ -f #{etc}/whatsapp-client/env ] && set -a && . #{etc}/whatsapp-client/env; exec #{opt_bin}/whatsapp-client daemon --log-file #{var}/log/whatsapp-client.log"]
    keep_alive true
    working_dir var/"whatsapp-client"
    log_path var/"log/whatsapp-client.log"
    error_log_path var/"log/whatsapp-client.log"
    environment_variables PATH: "#{HOMEBREW_PREFIX}/bin:/usr/bin:/bin"
  end

  def caveats
    <<~EOS
      whatsapp-client stores Signal credentials, history, and the owner-only Unix socket in:
        #{var}/whatsapp-client/

      Run only ONE whatsapp-client daemon against a given whatsapp.db.
      If you already manage com.bdombro.whatsapp-client via LaunchAgent, do not also start this Homebrew service.

      When unpaired, start whatsapp-mcp and connect via an MCP client: the OAuth browser flow
      displays the live WhatsApp QR code without needing terminal stdin.
    EOS
  end

  test do
    assert_match "WhatsApp", shell_output("#{bin}/whatsapp-client --help")
  end
end
