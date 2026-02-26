---
name: mermaid-to-svg
description: Convert a Mermaid `.mmd` file to SVG.
---
Convert a Mermaid `.mmd` file to SVG.

Usage: /mermaid-to-svg [path]

If no path argument is provided, default to `docs/architecture/overview.mmd` -> `docs/architecture/overview.svg` in the project root.

Steps:
1. Resolve the input `.mmd` file path (from argument or default).
2. Derive the output `.svg` path by replacing the `.mmd` extension with `.svg`.
3. **Click link substitution:** If the `.mmd` contains `click` directives with `.mmd"` targets, create a temp copy with `.mmd"` replaced by `.svg"` and use that as the mermaid-cli input. This keeps `.mmd` sources linking to `.mmd` (works in GitHub Mermaid rendering) while the generated `.svg` links to `.svg` files. Delete the temp file after generation.
4. Run: `npx --yes @mermaid-js/mermaid-cli -i <input.mmd> -o <output.svg> --backgroundColor transparent`
5. Validate the output with: `xmllint --noout <output.svg>`
6. Report the file size and confirm success.

Do NOT use the Mermaid Chart MCP tool — it produces SVG with unclosed `<br>` tags that fail XML validation. Always use the CLI.
