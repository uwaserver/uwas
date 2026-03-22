# Homebrew formula for UWAS
# Install: brew install uwaserver/tap/uwas

class Uwas < Formula
  desc "Unified Web Application Server — Apache+Nginx+Varnish+Caddy in one binary"
  homepage "https://github.com/uwaserver/uwas"
  license "Apache-2.0"
  version "1.0.1"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/uwaserver/uwas/releases/download/v#{version}/uwas_#{version}_darwin_arm64.tar.gz"
    else
      url "https://github.com/uwaserver/uwas/releases/download/v#{version}/uwas_#{version}_darwin_amd64.tar.gz"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/uwaserver/uwas/releases/download/v#{version}/uwas_#{version}_linux_arm64.tar.gz"
    else
      url "https://github.com/uwaserver/uwas/releases/download/v#{version}/uwas_#{version}_linux_amd64.tar.gz"
    end
  end

  def install
    bin.install "uwas"
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
