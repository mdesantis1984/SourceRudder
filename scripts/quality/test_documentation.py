import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
LINK_PATTERN = re.compile(r"(?<!!)\[[^]]*\]\(([^)]+)\)")
PAIRS = (
    ("README.md", "README.es.md"),
    ("CHANGELOG.md", "CHANGELOG.es.md"),
    ("CONTRIBUTING.md", "CONTRIBUTING.es.md"),
    ("SECURITY.md", "SECURITY.es.md"),
    ("ACKNOWLEDGEMENTS.md", "ACKNOWLEDGEMENTS.es.md"),
    ("CODE_OF_CONDUCT.md", "CODE_OF_CONDUCT.es.md"),
    ("MIGRATION-TO-2.0.md", "MIGRATION-TO-2.0.es.md"),
    ("docs/architecture.md", "docs/architecture.es.md"),
    ("docs/configuration.md", "docs/configuration.es.md"),
    ("docs/deployment.md", "docs/deployment.es.md"),
    ("docs/development.md", "docs/development.es.md"),
    ("docs/mcp-clients.md", "docs/mcp-clients.es.md"),
    ("docs/operations.md", "docs/operations.es.md"),
    ("docs/legal/README.md", "docs/legal/README.es.md"),
    ("docs/release/cutover.md", "docs/release/cutover.es.md"),
    ("docs/release/rollback.md", "docs/release/rollback.es.md"),
    ("deploy/quality/README.md", "deploy/quality/README.es.md"),
)


class DocumentationTests(unittest.TestCase):
    def test_readmes_are_product_led_and_show_release_status(self):
        expectations = {
            "README.md": ("## Why SourceRudder", "## Try it locally", "first public release"),
            "README.es.md": ("## Por qué SourceRudder", "## Pruébalo localmente", "primer release público"),
        }
        for relative, headings in expectations.items():
            with self.subTest(document=relative):
                body = (ROOT / relative).read_text(encoding="utf-8")
                self.assertIn("docs/assets/sourcerudder-social-preview.png", body)
                self.assertIn("actions/workflows/ci.yml/badge.svg?branch=main", body)
                self.assertIn("```mermaid", body)
                self.assertIn("28", body)
                self.assertIn("16", body)
                self.assertIn(headings[2], body.lower())
                self.assertLess(body.index(headings[0]), body.index(headings[1]))

    def test_local_markdown_links_resolve(self):
        broken = []
        for document in ROOT.rglob("*.md"):
            if any(part in {".agents", ".git", ".codegraph", ".codebase-memory"} for part in document.parts):
                continue
            for raw_target in LINK_PATTERN.findall(document.read_text(encoding="utf-8")):
                target = raw_target.split("#", 1)[0].strip()
                if not target or "://" in target or target.startswith(("mailto:", "#")):
                    continue
                path = ROOT / target.lstrip("/") if target.startswith("/") else document.parent / target
                if not path.resolve().exists():
                    broken.append(f"{document.relative_to(ROOT)} -> {raw_target}")
        self.assertEqual([], broken, "broken local Markdown links:\n" + "\n".join(broken))

    def test_bilingual_documents_expose_both_language_links(self):
        for english_path, spanish_path in PAIRS:
            with self.subTest(english=english_path, spanish=spanish_path):
                english = ROOT / english_path
                spanish = ROOT / spanish_path
                self.assertTrue(english.is_file(), f"missing {english_path}")
                self.assertTrue(spanish.is_file(), f"missing {spanish_path}")
                language_links = f"[English]({english.name}) | [Español]({spanish.name})"
                self.assertIn(language_links, english.read_text(encoding="utf-8"))
                self.assertIn(language_links, spanish.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
