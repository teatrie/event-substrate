Convert a Mermaid `.mmd` file to SVG.

Usage: /mermaid-to-svg [path]

If no path argument is provided, default to `architecture.mmd` -> `architecture.svg` in the project root.

Steps:
1. Resolve the input `.mmd` file path (from argument or default).
2. Derive the output `.svg` path by replacing the `.mmd` extension with `.svg`.
3. Run: `npx --yes @mermaid-js/mermaid-cli -i <input.mmd> -o <output.svg> --backgroundColor transparent`
4. Validate the output with: `xmllint --noout <output.svg>`
5. Report the file size and confirm success.

Do NOT use the Mermaid Chart MCP tool — it produces SVG with unclosed `<br>` tags that fail XML validation. Always use the CLI.
