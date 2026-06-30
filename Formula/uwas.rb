# Homebrew formula for UWAS
# Install: brew install uwaserver/tap/uwas

class Uwas < Formula
  desc "Unified Web Application Server — Apache+Nginx+Varnish+Caddy in one binary"
  homepage "https://github.com/uwaserver/uwas"
  license "Apache-2.0"
  version "0.8.8"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/uwaserver/uwas/releases/download/v#{version}/uwas-darwin-arm64"
      sha256 "8eeeb35cb25f02a1b20b5df358442c5064002d528a9dac750160b9cd347f3f1f"
    else
      url "https://github.com/uwaserver/uwas/releases/download/v#{version}/uwas-darwin-amd64"
      sha256 "6bc421a79b24aac5636ab34bd96f3816e068c7a95e06124a2c6939562c364916"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/uwaserver/uwas/releases/download/v#{version}/uwas-linux-arm64"
      sha256 "1f862cc2481879b586e40a8b677cf5e0a65f15b93dcb528c490b2adf7e57c05a"
    else
      url "https://github.com/uwaserver/uwas/releases/download/v#{version}/uwas-linux-amd64"
      sha256 "3e4713603d6a6cc8daa05aa24e43e168a84e3abe082911f8180831e5a78da894"
    end
  end

  def install
    downloaded = Dir["uwas-*"].first
    bin.install downloaded => "uwas"
  end

  def post_install
    (var/"lib/uwas/certs").mkpath
    (var/"cache/uwas").mkpath
    (var/"log/uwas").mkpath
  end

  service do
    run [opt_bin/"uwas", "serve", "-c", etc/"uwas/uwas.yaml"]
    keep_alive true
    working_dir var
    log_path var/"log/uwas/uwas.log"
    error_log_path var/"log/uwas/uwas.error.log"
  end

  test do
    assert_match "uwas", shell_output("#{bin}/uwas version")
  end
end
