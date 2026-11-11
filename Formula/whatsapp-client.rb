class WhatsappClient < Formula
  desc "WhatsApp sync daemon and CLI backed by whatsmeow"
  homepage "https://github.com/bdombro/whatsapp-mcp"
  version "2.0.0"
  license "MIT"

  url "https://github.com/bdombro/whatsapp-mcp/releases/download/v2.0.0/whatsapp-client-v2.0.0-darwin-arm64.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"

  def install
    bin.install "whatsapp-client"
  end

  service do
    run [opt_bin/"whatsapp-client", "daemon"]
    keep_alive true
    log_path var/"log/whatsapp-client.log"
    error_log_path var/"log/whatsapp-client.err.log"
  end

  def caveats
    <<~EOS
      whatsapp-client stores Signal credentials, history, and the owner-only Unix socket in:
        ~/.local/share/whatsapp-client/

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
