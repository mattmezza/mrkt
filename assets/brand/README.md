# mrkt identity

The mark combines a lowercase m, a branching message path and a deployment arrow. The lowercase wordmark is original path artwork, with no font dependency. All vector paths are editable. Use the cobalt mark on light paper, the pale variant on dark backgrounds, and the currentColor monochrome version when one ink is required. Keep one icon-width of clear space. Minimum icon size: 16 px; wordmark: 95 px wide.

UI tokens live in ../../tokens.css. Cobalt accent, cool paper and dark ink form the core palette. Space Grotesk supplies display typography; Inter supplies body typography. Fonts are locally hosted, obtained through pinned Fontsource packages under SIL OFL 1.1 (see THIRD_PARTY_NOTICES.md). The vector wordmark uses no third-party glyph outlines.

`cover.svg` is the editable 1280 × 640 README/social composition. `node scripts/export-brand.mjs` exports cover.png, social-preview.png and 32/180/192/512 px app icons. GitHub recommends 1280 × 640 and under 1 MB; the private repository’s image is retained locally for a later authorized public release. Source: https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/customizing-your-repositorys-social-media-preview

`message-sequence.png` is original AI-generated illustrative artwork from the built-in image-generation tool, visually inspected on 2026-09-10. It is illustrative, not an application screenshot. Prompt: “Three precisely folded off-white paper message cards arranged as a stepped sequence on a pale cool grey tabletop; one thin cobalt-blue thread connecting their folded corners; daylight, architectural material texture, asymmetric right-side objects and negative space on the left; no text, branding, UI, people, or sparkles.” The generated source is preserved unchanged. No third-party reference images were supplied.

Actual application screenshots are saved under screenshots/ by the browser acceptance tests. Never replace those with generated UI artwork.
