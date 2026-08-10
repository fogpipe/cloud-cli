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
  version "0.113.1"
  license :cannot_represent # proprietary binary; the packaging in this tap is MIT

  on_macos do
    on_arm do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-darwin-arm64"
      sha256 "a6af8162365a9f8095e5b463c6859f12c6197eaf789948a6ba33d53a12df2b36"
    end
    on_intel do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-darwin-amd64"
      sha256 "20ba9475de5e3edc97074a479c7910abb23d9265f912f07bad45513371783d1c"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-linux-arm64"
      sha256 "3c913be1b0dcdfda40ee609c392603889d59e7b0a9c37811d0ee8e98aaf74b6e"
    end
    on_intel do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-linux-amd64"
      sha256 "98ba6952fb6fffe73c9952c0f3fac20962d5bee1972d046c1a3981d00e14a53b"
    end
  end

  def install
    bin.install Dir["fpcloud-*"].first => "fpcloud"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/fpcloud version")
  end
end
