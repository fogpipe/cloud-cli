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
  version "0.88.0"
  license :cannot_represent # proprietary binary; the packaging in this tap is MIT

  on_macos do
    on_arm do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-darwin-arm64"
      sha256 "fa184626f00d37b8fb297c17b5c1aa71709df1808c7ef166528beddd2cc154f8"
    end
    on_intel do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-darwin-amd64"
      sha256 "c49f50e0e5bdafd1e38b5e98074ef9d1225a169bbf3bdc56c1c61d8887f94aaa"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-linux-arm64"
      sha256 "dbb37849ad9a7a646f53657d612003f8e242e5e9fb63d34487f0ede5ecd41783"
    end
    on_intel do
      url "https://github.com/fogpipe/cloud-cli/releases/download/v#{version}/fpcloud-linux-amd64"
      sha256 "a1ddde0f0643884523558965519f9d44997211921c795e67b800ff369c38e7a7"
    end
  end

  def install
    bin.install Dir["fpcloud-*"].first => "fpcloud"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/fpcloud version")
  end
end
