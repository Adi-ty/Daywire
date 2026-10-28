import math
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from types import SimpleNamespace as Token
import unittest
from unittest.mock import Mock

from generate import alignment, normalized, prepare_segments, pronunciation_text, validate_tokens


class SpeechContractTests(unittest.TestCase):
    def test_quote_spacing_preserves_contractions_numbers_and_all_characters(self):
        for quote in ("'", "‘", "’"):
            text = f"app.use({quote}/admin{quote},authMiddleware)"
            spaced = pronunciation_text(text)
            with self.subTest(quote=quote):
                self.assertIn(f"( {quote} /admin {quote} ,", spaced)
                self.assertEqual(normalized(spaced), normalized(text))
        text = "isn't isn’t O'Connor O’Connor users' 'tis 1.2.4 -1.5 <=2.0 /admin"
        self.assertEqual(pronunciation_text(text), text)

    def test_lost_contraction_apostrophe_or_code_character_still_rejected(self):
        for original, changed in [
            ("isn't", "isnt"), ("app.use('/admin',authMiddleware)", "app.use('admin',authMiddleware)"),
            ("version 1.2.4", "version 124"), ("-1.5", "1.5"), ("<=2.0", "<2.0"),
        ]:
            with self.subTest(original=original), self.assertRaisesRegex(ValueError, "changed the script"):
                validate_tokens(original, [Token(text=changed, whitespace="", phonemes="abc")])

    def test_code_quotes_survive_pronunciation_and_keep_original_word_cues(self):
        text = "Use scoped app.use('/admin',authMiddleware). It isn't public."
        pipeline = Mock()

        def g2p(value, *, preprocess):
            # Reproduce the reported loss: Misaki's subtoken regex cannot
            # match a single apostrophe between '(' and '/'. Spacing it as
            # its own token avoids that case without removing any character.
            value = value.replace("('/", "(/")
            tokens = [Token(text=m.group(1), whitespace=m.group(2), phonemes="abc")
                      for m in re.finditer(r"(\S+)(\s*)", value)]
            return "abc", tokens

        pipeline.g2p.side_effect = g2p
        pipeline.en_tokenize.side_effect = lambda tokens: [("", "abc", tokens)]
        chunks = prepare_segments(pipeline, text)[0]
        tokens = chunks[0][2]
        validate_tokens(text, tokens)
        self.assertIn("isn't", pipeline.g2p.call_args.args[0])
        timing = alignment(text, [(token.text, i, i + 0.5) for i, token in enumerate(tokens)])
        self.assertIsNotNone(timing)
        self.assertEqual(timing["characters"][::2], text.split())

    def test_runtime_check_loads_sibling_helper_in_isolated_python(self):
        runtime = Path(__file__).with_name("check_runtime.py").resolve()
        with tempfile.TemporaryDirectory(prefix="daywire-isolated-check-") as cwd:
            # Neither cwd nor PYTHONPATH should be needed to resolve the helper.
            Path(cwd, "generate.py").write_text("raise RuntimeError('wrong helper')\n")
            result = subprocess.run([
                sys.executable, "-I", "-c",
                "import pathlib, runpy, sys; "
                "namespace = runpy.run_path(sys.argv[1]); "
                "helper = namespace['prepare_segments']; "
                "assert pathlib.Path(helper.__globals__['__file__']).resolve() == "
                "pathlib.Path(sys.argv[1]).with_name('generate.py'); "
                "assert callable(namespace['check'])",
                str(runtime),
            ], cwd=cwd, capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_word_timing_preserves_punctuation_and_chunk_offsets(self):
        result = alignment("Node.js isn't slow.\n\nNext story.", [
            ("Node.js", 0.1, 0.6), ("is", 0.65, 0.7), ("n't", 0.7, 0.85),
            ("slow", 0.9, 1.2), (".", None, None),
            ("Next", 1.6, 1.9), ("story", 2.0, 2.3), (".", None, None),
        ])
        self.assertEqual(result["characters"][::2], ["Node.js", "isn't", "slow.", "Next", "story."])
        self.assertEqual(result["character_start_times_seconds"][::2], [0.1, 0.65, 0.9, 1.6, 2.0])
        self.assertEqual(result["character_end_times_seconds"][::2], [0.6, 0.85, 1.2, 1.9, 2.3])

    def test_no_invented_timing_for_missing_normalized_or_invalid_words(self):
        for tokens in [
            [("one", 0, 1)], [("one", 0, 1), ("too", 1, 2)],
            [("one", None, None), ("two", 1, 2)],
            [("one", math.nan, 1), ("two", 1, 2)],
            [("one", 2, 3), ("two", 1, 2)],
            [("one", -1, 1), ("two", 1, 2)],
        ]:
            with self.subTest(tokens=tokens):
                self.assertIsNone(alignment("one two", tokens))

    def test_whitespace_and_unicode(self):
        result = alignment("It’s\n ready!", [("It’s", 0, 1), ("ready", 1, 2), ("!", None, None)])
        self.assertEqual(result["characters"][::2], ["It’s", "ready!"])

    def test_missing_pronunciation_or_omitted_text_cannot_publish(self):
        tokens = [Token(text="UnusualAPI", whitespace="", phonemes="")]
        with self.assertRaisesRegex(ValueError, "no pronunciation"):
            validate_tokens("UnusualAPI", tokens)
        tokens[0].phonemes = "abc"
        with self.assertRaisesRegex(ValueError, "changed the script"):
            validate_tokens("UnusualAPI is new", tokens)
        validate_tokens("  UnusualAPI\n", tokens)

    def test_plain_text_bypasses_pronunciation_markup(self):
        text = "The label is [release](candidate)."
        token = Token(text=text, whitespace="", phonemes="abc")
        pipeline = Mock()
        pipeline.g2p.return_value = ("abc", [token])
        pipeline.en_tokenize.return_value = [(text, "abc", [token])]
        self.assertEqual(len(prepare_segments(pipeline, text)), 1)
        pipeline.g2p.assert_called_once_with(text, preprocess=False)

    def test_preflight_reports_later_block_without_accepting_text_loss(self):
        pipeline = Mock()
        def g2p(text, *, preprocess):
            self.assertFalse(preprocess)
            actual = text.replace("not ", "")
            return "abc", [Token(text=actual, whitespace="", phonemes="abc")]
        pipeline.g2p.side_effect = g2p
        pipeline.en_tokenize.side_effect = lambda tokens: [("", "abc", tokens)]
        with self.assertRaisesRegex(ValueError, "text block 3/3:.*expected.*notready.*received.*ready"):
            prepare_segments(pipeline, "First.\n\nSecond.\n\nIt is not ready.")

    def test_chunker_cannot_drop_or_change_script(self):
        pipeline = Mock()
        original = "version 1.2.4"
        pipeline.g2p.return_value = ("abc", [Token(text=original, whitespace="", phonemes="abc")])
        for changed in ["version 1.2", "version 1.2.5", ""]:
            pipeline.en_tokenize.return_value = [(changed, "abc", [Token(text=changed, whitespace="", phonemes="abc")])]
            with self.subTest(changed=changed), self.assertRaisesRegex(ValueError, "changed the script"):
                prepare_segments(pipeline, original)

    def test_preflight_checks_chunk_limits(self):
        token = Token(text="Ready", whitespace="", phonemes="abc")
        pipeline = Mock()
        pipeline.g2p.return_value = ("abc", [token])
        for chunks in [[], [("Ready", "", [token])], [("Ready", "x" * 511, [token])]]:
            pipeline.en_tokenize.return_value = chunks
            with self.subTest(chunks=chunks), self.assertRaisesRegex(ValueError, "unsupported phoneme chunk"):
                prepare_segments(pipeline, "Ready")


if __name__ == "__main__":
    unittest.main()
