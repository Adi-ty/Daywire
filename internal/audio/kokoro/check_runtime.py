"""Exercise installed dependencies without downloading speech model weights."""
import importlib.metadata
from pathlib import Path
import subprocess
import sys
import tempfile
import wave

from generate import prepare_segments


def check():
    # pip check validates declared metadata, not imports or native libraries.
    for line in Path(__file__).with_name("requirements.txt").read_text().splitlines():
        if "==" not in line or line.startswith("#"):
            continue
        name, version = line.split("==")
        name = name.split("[")[0]
        installed = importlib.metadata.version(name).split("+")[0]
        if installed != version:
            raise RuntimeError(f"{name}: expected {version}, found {installed}; rerun setup-kokoro.sh")
    import click  # noqa: F401
    import torch
    import spacy
    from kokoro import KPipeline
    from misaki import espeak
    from preshed.maps import PreshMap

    # spaCy hashes are unsigned 64-bit values. Import/pip checks cannot detect
    # a native wheel that incorrectly converts them to signed Py_ssize_t.
    hashes = PreshMap()
    for key in (2**63 + 1, 2**64 - 1):
        hashes[key] = 7
        if hashes[key] != 7:
            raise RuntimeError("native tokenizer hash-table check failed")
        del hashes[key]

    if not spacy.util.is_package("en_core_web_sm"):
        raise RuntimeError("English tokenizer missing; rerun setup-kokoro.sh")
    # Explicit espeak fallback prevents downloading a fallback neural model.
    for lang in ("a", "b"):
        espeak.EspeakFallback(british=lang == "b")
        # Exercise the same pipeline constructor as narration, without weights.
        pipeline = KPipeline(lang_code=lang, repo_id="hexgrad/Kokoro-82M", model=False)
        # Exercise the production plain-text path, including syntax Misaki's
        # default preprocessor would interpret as pronunciation instructions.
        prepare_segments(pipeline, "Daywire covers technology, Node.js and security updates.\n\n"
                         "It’s ready—version one point two point four.\n\n"
                         "The label is [release](candidate).")
    with tempfile.TemporaryDirectory(prefix="daywire-runtime-check-") as work:
        wav_path, mp3_path = Path(work) / "check.wav", Path(work) / "check.mp3"
        with wave.open(str(wav_path), "wb") as audio:
            audio.setparams((1, 2, 24000, 0, "NONE", "not compressed"))
            audio.writeframes(b"\0\0" * 2400)
        subprocess.run(["ffmpeg", "-nostdin", "-v", "error", "-i", str(wav_path),
                        "-codec:a", "libmp3lame", str(mp3_path)], check=True, timeout=15)
        duration = subprocess.check_output(["ffprobe", "-v", "error", "-show_entries", "format=duration",
                                            "-of", "default=noprint_wrappers=1:nokey=1", str(mp3_path)], timeout=15)
        if not 0 < float(duration) < 1:
            raise RuntimeError("MP3 encoder/decoder check failed")
    print(f"Runtime checks passed: imports, unsigned tokenizer hashes, American/British pronunciation, MP3 encoding; PyTorch {torch.__version__}.")
    print("Speech model weights have not been downloaded or tested by this check.")


if __name__ == "__main__":
    try:
        check()
    except Exception as error:
        print(f"Kokoro runtime check failed: {type(error).__name__}: {error}", file=sys.stderr)
        sys.exit(1)
