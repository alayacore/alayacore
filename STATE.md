<!-- STATE.md: Track active work only. No changelogs, no completed work. Keep under 50 lines. -->

Theme switching feature complete.

Implementation:
- RuntimeManager stores active_theme in runtime.conf
- ThemeManager loads themes from folder (default: ~/.alayacore/themes)
- ThemeSelector UI (Ctrl+P) with real-time preview
- Theme selector width matches model selector (both match input box width)
- CLI flag: --themes <folder> (replaces --theme <file>)
- Dynamic theme switching updates all UI components
- Theme persists across restarts
