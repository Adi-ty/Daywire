#!/usr/bin/env bash
# Hosted Ubuntu runner only. Reuse installed binaries to avoid downloading the
# entire FFmpeg dependency tree on every publication run.
set -euo pipefail

provider="${TTS_PROVIDER:-kokoro}"
case "$provider" in
  kokoro|elevenlabs) ;;
  *) echo "Unsupported TTS_PROVIDER: $provider" >&2; exit 1 ;;
esac

packages=()
if ! command -v ffmpeg >/dev/null || ! command -v ffprobe >/dev/null; then
  packages+=(ffmpeg)
fi
if [[ "$provider" == kokoro ]] && ! command -v espeak-ng >/dev/null; then
  packages+=(espeak-ng)
fi
if [[ ${#packages[@]} -eq 0 ]]; then
  echo 'System speech tools already installed.'
  exit 0
fi

# Retry transient download failures, bound stalled connections, and reject
# partial index refreshes instead of silently using stale package lists.
apt_options=(-o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30)
sudo apt-get "${apt_options[@]}" -o APT::Update::Error-Mode=any update
sudo apt-get "${apt_options[@]}" install -y --no-install-recommends "${packages[@]}"

command -v ffmpeg >/dev/null
command -v ffprobe >/dev/null
if [[ "$provider" == kokoro ]]; then
  command -v espeak-ng >/dev/null
fi
