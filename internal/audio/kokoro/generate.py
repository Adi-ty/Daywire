"""Local narration helper. Receives private JSON; returns MP3 + optional timing.

No web service, API credentials, editorial rewriting or estimated reading-rate
timestamps. The Go caller owns deadlines, caching, publication and ffprobe checks.
"""
import argparse
import base64
import json
import math
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import wave


def log(message):
    print(f"Kokoro: {message}", file=sys.stderr, flush=True)


def normalized(text):
    return "".join(text.split())


def validate_tokens(text, tokens):
    # Misaki supports markup that can change/omit input. Our script is plain text:
    # never silently lose words or accept the library's long-token truncation.
    if normalized("".join(t.text + t.whitespace for t in tokens)) != normalized(text):
        raise ValueError("pronunciation tokenization changed the script; revise that radio segment")
    if any(any(c.isalnum() for c in t.text) and not t.phonemes for t in tokens):
        raise ValueError("a script word has no pronunciation; revise that radio segment")


def alignment(text, timed_tokens):
    """Map model durations back to exact whitespace-delimited transcript words.

    A word can span several tokens (e.g. Node.js or contractions). Punctuation
    without a duration inherits its word's span, never a guessed reading rate.
    Any text or timing mismatch disables following for the whole recording.
    """
    letters, times = [], []
    for token, start, end in timed_tokens:
        for char in token:
            if not char.isspace():
                letters.append(char)
                times.append((start, end))
    if "".join(letters) != normalized(text):
        return None
    result = {"characters": [], "character_start_times_seconds": [], "character_end_times_seconds": []}
    offset, previous = 0, -1.0
    for word in text.split():
        spans = times[offset:offset + len(word)]
        offset += len(word)
        valid = []
        for char, (start, end) in zip(word, spans):
            if start is None or end is None:
                if char.isalnum():
                    return None
                continue
            if not math.isfinite(start) or not math.isfinite(end) or start < 0 or end < start:
                return None
            valid.append((start, end))
        if not valid:
            return None
        start, end = min(s[0] for s in valid), max(s[1] for s in valid)
        if start < previous:
            return None
        previous = start
        result["characters"].extend([word, " "])
        result["character_start_times_seconds"].extend([start, start])
        result["character_end_times_seconds"].extend([end, end])
    return result


def synthesize(request, destination):
    text, voice, speed = request["text"], request["voice"], request["speed"]
    device = request.get("device", "cpu")
    if not isinstance(text, str) or not text.strip() or len(text) > 16000:
        raise ValueError("script must contain 1–16000 characters")
    if not re.fullmatch(r"[ab][fm]_[a-z]+", voice) or not 0.75 <= speed <= 1.5:
        raise ValueError("invalid English voice or speed")
    if device not in ("cpu", "mps"):
        raise ValueError("device must be cpu or mps")
    if device == "mps":
        # Must be set before importing torch. Unsupported MPS operations may use CPU.
        os.environ["PYTORCH_ENABLE_MPS_FALLBACK"] = "1"
    # Heavy imports are deliberately lazy so contract tests need only stdlib.
    import numpy as np
    import torch
    import spacy
    from huggingface_hub import hf_hub_download
    from kokoro import KModel, KPipeline
    from misaki import espeak

    torch.set_num_threads(2)
    torch.manual_seed(0)
    if device == "mps" and not torch.backends.mps.is_available():
        raise RuntimeError("MPS is unavailable; use KOKORO_DEVICE=cpu")
    if not spacy.util.is_package("en_core_web_sm"):
        raise RuntimeError("missing English tokenizer; rerun scripts/setup-kokoro.sh")
    # Fail early if espeak is unavailable; don't trigger a surprise fallback-model download.
    espeak.EspeakFallback(british=voice.startswith("b"))
    recipe = json.loads(Path(__file__).with_name("model.json").read_text())

    def download(name):
        return hf_hub_download(repo_id=recipe["repo"], revision=recipe["revision"], filename=name, token=False)

    # Tokenizer/native dependency errors must fail before expensive downloads.
    log("initializing English pronunciation")
    pipeline = KPipeline(lang_code=voice[0], repo_id=recipe["repo"], model=False)
    log("loading pinned model (first run downloads weights)")
    model = KModel(repo_id=recipe["repo"], config=download("config.json"), model=download(recipe["weights"])).to(device).eval()
    log(f"loading voice {voice}")
    voice_pack = torch.load(download(f"voices/{voice}.pt"), map_location="cpu", weights_only=True)
    sample_rate = recipe["sample_rate"]
    tokens_with_times = []
    segments = [part for part in re.split(r"\n\s*\n", text) if part.strip()]
    samples = 0
    with tempfile.TemporaryDirectory(prefix="daywire-wave-", dir=destination.parent) as work:
        wav_path, mp3_path = Path(work) / "speech.wav", Path(work) / "speech.mp3"
        with wave.open(str(wav_path), "wb") as wav:
            wav.setnchannels(1)
            wav.setsampwidth(2)
            wav.setframerate(sample_rate)

            def append_audio(audio):
                nonlocal samples
                if audio.ndim != 1 or not audio.size or not np.isfinite(audio).all():
                    raise ValueError("invalid generated waveform")
                samples += len(audio)
                if samples > sample_rate * 300:
                    raise ValueError("audio exceeds five minutes; revise the script before regenerating")
                wav.writeframes((np.clip(audio, -1, 1) * 32767).astype("<i2").tobytes())

            for index, segment in enumerate(segments):
                log(f"segment {index + 1}/{len(segments)}")
                _, tokens = pipeline.g2p(segment)
                validate_tokens(segment, tokens)
                chunks = list(pipeline.en_tokenize(tokens))
                if not chunks or any(not ps or len(ps) > 510 for _, ps, _ in chunks):
                    raise ValueError("unsupported phoneme chunk; revise the script (no truncation performed)")
                for chunk_index, (_, phonemes, chunk_tokens) in enumerate(chunks):
                    if index > 0 and chunk_index == 0:
                        append_audio(np.zeros(round(0.3 * sample_rate)))
                    elif chunk_index > 0:
                        append_audio(np.zeros(round(0.12 * sample_rate)))
                    offset = samples / sample_rate
                    with torch.inference_mode():
                        output = pipeline.infer(model, phonemes, voice_pack, speed)
                    if output.pred_dur is not None:
                        pipeline.join_timestamps(chunk_tokens, output.pred_dur)
                    audio = output.audio.detach().cpu().numpy()
                    if not np.any(np.abs(audio) > 1e-5):
                        raise ValueError("model produced silence")
                    append_audio(audio)
                    for token in chunk_tokens:
                        start, end = token.start_ts, token.end_ts
                        tokens_with_times.append((token.text, None if start is None else offset + start, None if end is None else offset + end))

        # Encode the complete waveform once; concatenating MP3 fragments introduces
        # encoder delays at every boundary and can desynchronize the transcript.
        subprocess.run(["ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", str(wav_path),
                        "-map_metadata", "-1", "-codec:a", "libmp3lame", "-b:a", "128k", str(mp3_path)],
                       check=True, timeout=60)
        audio = mp3_path.read_bytes()
        if not 0 < len(audio) <= 12 << 20:
            raise ValueError("encoded audio size is invalid")
        timing = alignment(text, tokens_with_times)
        if timing is None:
            log("word timing unavailable; publishing a readable transcript without word following")
        response = {"audio_base64": base64.b64encode(audio).decode("ascii"), "alignment": timing}
        temporary = destination.with_suffix(".tmp")
        temporary.write_text(json.dumps(response, allow_nan=False), encoding="utf-8")
        os.replace(temporary, destination)
        log(f"complete: {samples / sample_rate:.1f}s, {len(audio)} bytes")


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser()
    parser.add_argument("--request", required=True, type=Path)
    parser.add_argument("--response", required=True, type=Path)
    args = parser.parse_args()
    if args.request.stat().st_size > 128 * 1024:
        raise ValueError("speech request exceeds size limit")
    synthesize(json.loads(args.request.read_text(encoding="utf-8")), args.response)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        log(f"failed: {type(error).__name__}: {error}")
        sys.exit(1)
