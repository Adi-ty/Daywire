#!/usr/bin/env bash
# Owns only the repository's .venv-kokoro and .cache/kokoro directories.
# Never installs/uninstalls Homebrew packages or changes shell configuration.
set -euo pipefail
cd "$(dirname "$0")/.."

usage() {
  echo 'Usage: bash scripts/setup-kokoro.sh [--check | --remove | --help]'
  echo 'No argument: install/repair and verify the local speech runtime.'
  echo '--check: verify the existing runtime without installing/downloading anything.'
  echo '--remove: remove only .venv-kokoro and .cache/kokoro after testing.'
}
[[ $# -le 1 ]] || { usage >&2; exit 2; }
action="${1:-install}"
case "$action" in
  --help|-h) usage; exit 0 ;;
  install|--check|--remove) ;;
  *) usage >&2; exit 2 ;;
esac

if [[ "$action" == --remove ]]; then
  # Validate BOTH paths before deleting anything. Never follow an external
  # symlink, including a symlinked .cache parent. No global cache purge.
  python3 - <<'PY'
from pathlib import Path
import shutil
root = Path.cwd().resolve()
targets = [root / '.venv-kokoro', root / '.cache/kokoro']
if (root / '.cache').is_symlink():
    raise SystemExit('Refusing cleanup through a symlinked .cache directory')
for target in targets:
    if target.is_symlink() or not target.resolve().is_relative_to(root):
        raise SystemExit(f'Refusing cleanup of redirected path: {target}')
    if target.exists() and not target.is_dir():
        raise SystemExit(f'Refusing cleanup of unexpected file: {target}')
for target in targets:
    if target.exists():
        shutil.rmtree(target)
        print(f'Removed {target.relative_to(root)}')
print('Daywire runtime removed. Homebrew, editions, .env, checkpoints and R2 are unchanged.')
PY
  exit 0
fi

[[ ! -L .venv-kokoro && ! -L .cache && ! -L .cache/kokoro ]] || {
  echo 'Refusing setup through symlinked runtime/cache directories.' >&2; exit 1;
}
for executable in ffmpeg ffprobe; do
  command -v "$executable" >/dev/null || {
    echo "Missing $executable. On macOS install FFmpeg with: brew install ffmpeg" >&2; exit 1;
  }
done

check_runtime() {
  [[ -x .venv-kokoro/bin/python ]] || { echo 'Runtime missing; run bash scripts/setup-kokoro.sh first.' >&2; return 1; }
  .venv-kokoro/bin/python -I -m pip --isolated --disable-pip-version-check --no-cache-dir check
  HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 .venv-kokoro/bin/python -I internal/audio/kokoro/check_runtime.py
}
if [[ "$action" == --check ]]; then
  check_runtime
  exit 0
fi

kokoro_python="${KOKORO_SETUP_PYTHON:-python3.12}"
command -v "$kokoro_python" >/dev/null || {
  echo 'Python 3.12 missing. On macOS: brew install python@3.12; or set KOKORO_SETUP_PYTHON to Python 3.11/3.12.' >&2; exit 1;
}
"$kokoro_python" -I -c 'import sys; sys.exit(0 if (3, 11) <= sys.version_info[:2] <= (3, 12) else "Use Python 3.11 or 3.12 for Kokoro")'
if [[ -d .venv-kokoro ]]; then
  [[ -f .venv-kokoro/pyvenv.cfg && -x .venv-kokoro/bin/python ]] || {
    echo 'Existing runtime is incomplete; use --remove before reinstalling.' >&2; exit 1;
  }
  .venv-kokoro/bin/python -I -c 'import sys; sys.exit(0 if sys.version_info[:2] == tuple(map(int, sys.argv[1].split("."))) else "Existing runtime uses a different Python version; use --remove before reinstalling")' \
    "$("$kokoro_python" -I -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')"
else
  "$kokoro_python" -I -m venv .venv-kokoro
fi

kokoro_cache="$PWD/.cache/kokoro/pip"
if [[ "${GITHUB_ACTIONS:-}" == true ]]; then
  kokoro_cache="${PIP_CACHE_DIR:-$HOME/.cache/pip}"
fi
# Ignore user pip settings and env such as PIP_NO_DEPS/PIP_TARGET.
# Every install goes through this virtualenv, never the system interpreter.
pip_install=(.venv-kokoro/bin/python -I -m pip --isolated --disable-pip-version-check --cache-dir "$kokoro_cache" --retries 2 --timeout 30 install)
if [[ "$(uname -s)" == Linux ]]; then
  "${pip_install[@]}" 'torch==2.10.0' --index-url https://download.pytorch.org/whl/cpu
fi
"${pip_install[@]}" --index-url https://pypi.org/simple -r internal/audio/kokoro/requirements.txt
check_runtime
echo 'Kokoro runtime ready. Model and voice download on first narration.'
echo 'After testing: bash scripts/setup-kokoro.sh --remove'
