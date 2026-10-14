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
  version "0.212.0"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-darwin-arm64"
      sha256 "4f1cd3f3c298123b2859f5c5fd49e8cee6ca23b9af26a37d950adcb0c244ec55"
    end
    on_intel do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-darwin-amd64"
      sha256 "7a02eb508c6d91a1becf3ffca0eca558bdad6671135c448c797a6f5616345962"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-linux-arm64"
      sha256 "6c311ab86bcdb7450e602fbd2cfa9ab538634d674f64f8a886f6b115430add91"
    end
    on_intel do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-linux-amd64"
      sha256 "401143c0ddb222de657a2a94ef182a66778ec6d5f5dedecfde4e0f0b28146ee9"
    end
  end

  def install
    bin.install Dir["fpcloud-*"].first => "fpcloud"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/fpcloud version")
  end
end
