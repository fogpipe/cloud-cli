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
  version "0.68.0"
  license :cannot_represent # proprietary binary; the packaging in this tap is MIT

  on_macos do
    on_arm do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-darwin-arm64"
      sha256 "f9c9d79f78cba7d28b04270b0819781ab393944618477106d47f499448529867"
    end
    on_intel do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-darwin-amd64"
      sha256 "258360127b5a3f9e7a1194d9fa011480d962cab9acf9b9dd06cab3bbf1cbdcab"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-linux-arm64"
      sha256 "e5bf9df3150998a3e02bc4ba89d5ed985cad98a754c89b8c96c02da25c5e700e"
    end
    on_intel do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-linux-amd64"
      sha256 "1e21d9e9302b663dcb76b50b5d01f83f5b2e5e06cec1d8665f53208d4234d899"
    end
  end

  def install
    bin.install Dir["fpcloud-*"].first => "fpcloud"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/fpcloud version")
  end
end
