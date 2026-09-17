# Homebrew formula for the fpcloud CLI (prebuilt proprietary binary).
# Tap + install:
#   brew tap fogpipe/cloud-cli https://github.com/fogpipe/cloud-cli
#   brew install fogpipe/cloud-cli/fpcloud
#
# version + the four sha256 values are rewritten by the release pipeline
# (release-fpcloud.yml) on every tag; the placeholders below are intentional.
class Fpcloud < Formula
  desc "Fogpipe Cloud CLI — deploy apps, manage databases, domains, and object storage"
  homepage "https://github.com/fogpipe/cloud-cli"
  version "0.190.3"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-darwin-arm64"
      sha256 "52abdc26e8254e7fe68eb2e7ab49a5209795f629d10b7f3bfbc30eb74cf18f42"
    end
    on_intel do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-darwin-amd64"
      sha256 "62303c8b0ebad3e6c3793a238a645915a85b8370a3027d3e0a119d17a4edf2b7"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-linux-arm64"
      sha256 "54e399e34229f6fb920f382f9d43129daca62f49d50b67aa34bbc80d1514b241"
    end
    on_intel do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-linux-amd64"
      sha256 "a7e6216ab31c1a0e29f077f045ea31e72c03e488f7dec1850317c4395cfca0be"
    end
  end

  def install
    bin.install Dir["fpcloud-*"].first => "fpcloud"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/fpcloud version")
  end
end
