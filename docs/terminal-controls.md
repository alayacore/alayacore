# Terminal Controls

When running the Terminal version:

## Navigation

| Key | Action |
|-----|--------|
| `Tab` | Switch focus between display and input window |
| `j` | Move window cursor down (when display focused) |
| `k` | Move window cursor up (when display focused) |
| `J` | Move screen down (when display focused) |
| `K` | Move screen up (when display focused) |
| `g` | Go to first window and top of display (when display focused) |
| `G` | Go to last window and bottom of display (when display focused) |
| `H` | Move cursor to window at top of visible area (when display focused) |
| `L` | Move cursor to window at bottom of visible area (when display focused) |
| `M` | Move cursor to window at center of visible area (when display focused) |

## Input & Actions

| Key | Action |
|-----|--------|
| `Enter` | Submit prompt (when input focused) |
| `Ctrl+S` | Save session to file |
| `Ctrl+O` | Open external editor for multi-line input |
| `Ctrl+L` | Open model selector UI |
| `:` | Switch to input with ":" prefix (when display focused) |
| `Space` | Toggle wrap mode for active window (when display focused) |
| `Ctrl+C` | Clear input (when input focused) |
| `Ctrl+G` | Cancel current request (with confirmation) |

## Commands

| Command | Action |
|---------|--------|
| `:cancel` | Cancel current request (with confirmation) |
| `:quit`, `:q` | Exit with confirmation (press y/n) |
| `:model_set <id>` | Switch to a saved model configuration |
| `:model_load` | Load model configurations from default config file |
