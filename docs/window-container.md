# Window Container

AlayaCore's terminal adaptor organizes concurrent streams into separate windows with synchronized widths. Each stream (reasoning, text, tool outputs) appears in its own window with dimmed borders.

## Features

- **Stream ID suffix**: To prevent collisions across conversation turns, stream IDs include a monotonic suffix (e.g., `0-1`, `1-1` for first turn; `0-2`, `1-2` for second turn). This ensures each turn's content appears in distinct windows while keeping related deltas grouped within a turn.
- **Width synchronization**: All windows match the input box width for consistent layout.
- **Delta routing**: Content with stream ID prefix `[:id:]` is routed to the appropriate window via `parseStreamID()`.

## Window Cursor

The Window Cursor highlights one window in the display area with a brighter border. Use `j` and `k` keys to navigate between windows.

- **Default position**: The cursor defaults to the last window and updates automatically when new windows are created.
- **Auto-follow**: When new message windows are appended, cursor moves to the new window and viewport scrolls to bottom. Leaving the last window (k, g, H, L, M, etc.) disables follow; pressing G or j to return to the last window re-enables it.
- **Focus-dependent highlighting**: The orange border cursor only appears when the display area is focused (Tab to switch between display and input).
- **Highlighted border**: The selected window displays an orange border (`#fab387`) when the display area is focused. Unselected windows have invisible borders that match the background, preserving layout when windows are selected.
- **Wrap mode**: Press `Space` to toggle the active window between normal and wrap mode. In wrap mode, the window shows only the last 3 lines of content, displaying the newest content. Wrapped windows display a `Wrapped - Space to expand` indicator with a subtle background color.
