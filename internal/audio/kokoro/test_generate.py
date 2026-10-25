import math
from types import SimpleNamespace as Token
import unittest

from generate import alignment, validate_tokens


class SpeechContractTests(unittest.TestCase):
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


if __name__ == "__main__":
    unittest.main()
