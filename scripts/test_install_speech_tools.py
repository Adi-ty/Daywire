"""Exercise runner provisioning without network access or package installation."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class SpeechToolsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="daywire-speech-tools-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.log = self.root / "calls.jsonl"
        self.script = Path(__file__).with_name("install-speech-tools.sh").resolve()
        sudo = self.root / "sudo"
        sudo.write_text(f"#!{sys.executable}\n" + '''import json, os, pathlib, sys
root = pathlib.Path(__file__).parent
args = sys.argv[1:]
with (root / "calls.jsonl").open("a") as log:
    log.write(json.dumps(args) + "\\n")
if os.environ.get("FAIL_APT") in args:
    sys.exit(100)
if "install" in args:
    names = (["ffmpeg", "ffprobe"] if "ffmpeg" in args else [])
    names += ["espeak-ng"] if "espeak-ng" in args else []
    for name in names:
        target = root / name
        target.write_text("#!/bin/sh\\nexit 0\\n")
        target.chmod(0o755)
''')
        sudo.chmod(0o755)

    def installed(self, *names):
        for name in names:
            path = self.root / name
            path.write_text("#!/bin/sh\nexit 0\n")
            path.chmod(0o755)

    def run_setup(self, provider="kokoro", fail=""):
        env = {**os.environ, "PATH": str(self.root), "TTS_PROVIDER": provider, "FAIL_APT": fail}
        result = subprocess.run(["/bin/bash", str(self.script)], env=env,
                                capture_output=True, text=True, timeout=10)
        calls = [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []
        return result, calls

    def test_ready_runner_never_contacts_package_mirror(self):
        self.installed("ffmpeg", "ffprobe", "espeak-ng")
        result, calls = self.run_setup()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, [])

    def test_only_missing_espeak_is_installed_with_download_guards(self):
        self.installed("ffmpeg", "ffprobe")
        result, calls = self.run_setup()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(calls), 2)
        self.assertEqual(calls[1][-4:], ["install", "-y", "--no-install-recommends", "espeak-ng"])
        self.assertIn("APT::Update::Error-Mode=any", calls[0])
        for call in calls:
            for option in ["Acquire::Retries=3", "Acquire::http::Timeout=30", "Acquire::https::Timeout=30"]:
                self.assertIn(option, call)

    def test_missing_ffprobe_repairs_ffmpeg_package(self):
        self.installed("ffmpeg", "espeak-ng")
        result, calls = self.run_setup()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls[1][-1], "ffmpeg")
        self.assertNotIn("espeak-ng", calls[1])

    def test_elevenlabs_needs_no_espeak(self):
        self.installed("ffmpeg", "ffprobe")
        result, calls = self.run_setup("elevenlabs")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, [])

    def test_cold_runner_installs_both_packages(self):
        result, calls = self.run_setup()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls[1][-2:], ["ffmpeg", "espeak-ng"])

    def test_update_failure_stops_before_install(self):
        result, calls = self.run_setup(fail="update")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(calls), 1)

    def test_install_failure_is_not_reported_as_ready(self):
        result, calls = self.run_setup(fail="install")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(calls), 2)


if __name__ == "__main__":
    unittest.main()
