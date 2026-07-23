# Homebrew formula for the fpcloud CLI (prebuilt proprietary binary).
# Tap + install:
#   brew tap fogpipe/cloud-cli https://github.com/fogpipe/cloud-cli
#   brew install fogpipe/cloud-cli/fpcloud
#
# version + the four sha256 values are rewritten by the release pipeline
# (release-fpcloud.yml) on every tag; the placeholders below are intentional.
class Fpcloud < Formula
  desc "Fogpipe Cloud CLI — deploy apps, manage databases, scoped kubectl access"
  homepage "https://github.com/fogpipe/cloud-cli"
  version "0.70.0"
  license :cannot_represent # proprietary binary; the packaging in this tap is MIT

  on_macos do
    on_arm do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-darwin-arm64"
      sha256 "8026f4b48c7e9dd0da402734b9d1fc67d7978c6aee6b63babfe5c0021662233b"
    end
    on_intel do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-darwin-amd64"
      sha256 "38e84138a89489a100feb217b849c1b7076e37f3ac09eb8e8674f081e9417df1"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-linux-arm64"
      sha256 "830e06ae21b1660da95839b7d88c1097c98ab8ef2b13a60b0ba996975d710804"
    end
    on_intel do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-linux-amd64"
      sha256 "acf7c8bb37c23b92a5c8603b76d5facfbc863802de313297733d13711f2243d1"
    end
  end

  def install
    bin.install Dir["fpcloud-*"].first => "fpcloud"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/fpcloud version")
  end
end
