"""Cleanup regression tests use disposable directories, never the real runtime."""
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class SetupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="daywire-setup-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "project"
        (self.root / "scripts").mkdir(parents=True)
        self.script = self.root / "scripts/setup-kokoro.sh"
        shutil.copyfile(Path(__file__).with_name("setup-kokoro.sh"), self.script)

    def run_script(self, *args):
        # Also verifies resolving the repository root from a different cwd.
        return subprocess.run(["bash", str(self.script), *args], cwd=self.temp.name,
                              capture_output=True, text=True, timeout=10)

    def test_cleanup_preserves_editions_state_and_other_caches(self):
        for folder in [".venv-kokoro", ".cache/kokoro", ".cache/other", "content/editions", ".daywire/audio"]:
            path = self.root / folder
            path.mkdir(parents=True, exist_ok=True)
            (path / "sentinel").write_text("keep unless owned")
        (self.root / ".env").write_text("sentinel")
        result = self.run_script("--remove")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.root / ".venv-kokoro").exists())
        self.assertFalse((self.root / ".cache/kokoro").exists())
        for folder in [".cache/other", "content/editions", ".daywire/audio"]:
            self.assertTrue((self.root / folder / "sentinel").exists())
        self.assertTrue((self.root / ".env").exists())
        self.assertEqual(self.run_script("--remove").returncode, 0)

    def test_redirected_cache_aborts_before_removing_environment(self):
        outside = Path(self.temp.name) / "outside"
        (outside / "kokoro").mkdir(parents=True)
        (outside / "kokoro/sentinel").write_text("keep")
        (self.root / ".cache").symlink_to(outside, target_is_directory=True)
        (self.root / ".venv-kokoro").mkdir()
        self.assertNotEqual(self.run_script("--remove").returncode, 0)
        self.assertTrue((outside / "kokoro/sentinel").exists())
        self.assertTrue((self.root / ".venv-kokoro").exists())

    def test_symlinked_runtime_is_not_removed(self):
        target = self.root / "unrelated"
        target.mkdir()
        (self.root / ".venv-kokoro").symlink_to(target, target_is_directory=True)
        self.assertNotEqual(self.run_script("--remove").returncode, 0)
        self.assertTrue(target.exists())

    def test_unknown_argument_does_not_start_installation(self):
        self.assertEqual(self.run_script("--unexpected").returncode, 2)
        self.assertFalse((self.root / ".venv-kokoro").exists())


if __name__ == "__main__":
    unittest.main()
