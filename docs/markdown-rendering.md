# Markdown Rendering

Assistant text and reasoning windows render markdown tables. Everything else —
bold, italics, inline code, headings, lists — passes through as written, because
streaming content deliberately carries no styling: the renderer owns all color
(`bodyStyled` / `styleBodyLines`), so any SGR escaping from the table transform
would desynchronize the dim overlay coloring and break the incremental wrap path.

The table transform is line-based and fence-aware: a table inside a ``` fenced
code block is left exactly as typed.

## Turning it on and off

| | |
|---|---|
| Default | **on** for `ASSISTANT` and `REASONING` windows |
| `r` | toggle raw ↔ rendered, per window, only while unfolded |
| `--no-markdown` | start new windows raw instead |
| Folded windows | always raw: the one-line summary is not a table |

Folded and rendered state are independent per window, so `r` on one window does
not change any other.

## Wide enough: a grid

When the window can hold the table, it is drawn as a unicode grid with aligned
columns, and a rule between every pair of rows:

<!-- @example src=models width=80 -->
```
┌───────────┬─────────┬─────────┐
│ Model     │ Context │ Price   │
├───────────┼─────────┼─────────┤
│ qwen3:32b │ 128K    │ local   │
├───────────┼─────────┼─────────┤
│ llama3.1  │ 8K      │ local   │
├───────────┼─────────┼─────────┤
│ gpt-4o    │ 128K    │ $2.50/M │
└───────────┴─────────┴─────────┘
```

## Tighter: rows grow taller, nothing is cut

A table wider than the window is re-flowed, never truncated. Columns are
allocated by marginal gain — each spare cell goes to the column where it removes
the most wrapped lines — so text-heavy columns widen first and short ones like
`Size` and `Used` are never padded into uselessness. A cell that no longer fits is
hard wrapped with the same primitive ordinary body text uses (`wrapContent` →
`ansi.Hardwrap`: character boundaries, no word detection), so one record may span
several rows and the horizontal rule is what marks where it ends:

<!-- @example src=df width=62 -->
```
┌───────────────┬──────────────────────┬───────┬──────┬──────┐
│ Filesystem    │ Mounted on           │ Type  │ Size │ Used │
├───────────────┼──────────────────────┼───────┼──────┼──────┤
│ /dev/nvme0n1p │ /home/wallace/projec │ ext4  │ 916G │ 703G │
│ 2             │ ts/alayacore         │       │      │      │
├───────────────┼──────────────────────┼───────┼──────┼──────┤
│ tmpfs         │ /run/user/1000/doc   │ tmpfs │ 16G  │ 2.1G │
└───────────────┴──────────────────────┴───────┴──────┴──────┘
```

Width is measured in display columns, so mixed-script tables align:

<!-- @example src=cjk width=62 -->
```
┌───────────┬────────┬─────────────────────────────────────┐
│ 模型      │ 上下文 │ 说明                                │
├───────────┼────────┼─────────────────────────────────────┤
│ qwen3:32b │   128K │ 本地跑，需要 20G 显存，工具调用稳定 │
├───────────┼────────┼─────────────────────────────────────┤
│ llama3.1  │     8K │ 只适合短任务                        │
└───────────┴────────┴─────────────────────────────────────┘
```

## Too narrow for a frame: the record form

Each record becomes a block of fields, records separated by a plain rule in the
app's open-box style:

<!-- @example src=ps width=40 -->
```
  PID  1
  USER  root
  %CPU  0.0
  %MEM  0.1
  VSZ  169444
  RSS  13204
  TTY  ?
  STAT  Ss
  START  Jan12
  TIME  0:12
────────────────────────────────────────
  PID  2888
  USER  wallace
  %CPU  3.2
  %MEM  4.8
  VSZ  1248320
  RSS  312880
  TTY  pts/3
  STAT  Rl+
  START  09:14
  TIME  4:31
```

A field shares one line as `Field  value` **only when the whole field fits**.
Otherwise the label takes its own line and the value starts beneath it, which is
what keeps a label from being severed with the value glued to its remainder:

<!-- @example src=df width=20 -->
```
  Filesystem
    /dev/nvme0n1p2
  Mounted on
    /home/wallace/pr
    ojects/alayacore
  Type  ext4
  Size  916G
  Used  703G
────────────────────
  Filesystem  tmpfs
  Mounted on
    /run/user/1000/d
    oc
  Type  tmpfs
  Size  16G
  Used  2.1G
```

There is no label column. A fixed label column charges its width to every line;
in an 18-column window with 16-column labels that leaves a single cell for values
— one character per line. With no column to keep aligned, the record form needs no
column budget at all, so it works at any width.

Indentation is chrome, so it is given up **entirely** before any content is pushed
past the window edge. That is why no rendered line exceeds the window all the way
down to one column; the only tolerated overflow is a single grapheme cluster
wider than the window itself (a CJK ideograph in a one-column window), which no
wrapping can fit.

## Where the frame gives up

This is arithmetic, not taste. A column must be able to hold the widest unbreakable
grapheme cluster it contains — one cell for ASCII, two for a CJK ideograph, which
cannot be split. The frame is therefore possible while:

```
maxWidth >= 3n + 1 + (sum over columns of that column's widest cluster)
                ^^^^^^^ n+1 rules and 2n padding spaces
```

Verified against the renderer:

| Table | Frame from | Below that |
|---|---|---|
| 2 ASCII columns | 9 cells | record form |
| 3 ASCII columns | 13 cells | record form |
| 3 CJK columns | 16 cells | record form |
| 5 ASCII columns | 21 cells | record form |
| 10 ASCII columns (`ps`) | **41 cells** | record form |

The last row is the reason the record form exists. Forty columns is an ordinary
split pane, and `ps`-shaped output is exactly what an agent pastes into a
message, so this is not a defence against a hypothetical terminal.

## What framing costs at the limit

The bound says when a frame is *possible*, not when it is pleasant. At the exact
bound every column holds one character, and the result is a character grid rather
than a table. Same content, measured rows:

| Window | Form | Rows |
|---|---|---|
| 21 (bound for 5 ASCII columns) | grid | 64 |
| 22 | grid | 39 |
| 20 | record | 16 |

This is the deliberate price of always keeping the frame whenever it is drawable.
The alternative — always framing, even where the frame cannot fit inside the
window — was measured and rejected: at 40 columns a ten-column table renders 15
of its 17 rows wider than the window, and the ordinary pipeline tears the box
apart with the right-hand border landing on a wrap point.

## What never happens

- **No truncation.** No `…` is ever inserted into a table cell. (The previous
  design shrank columns and cut cells to hold every row to one visual line; that
  invariant, and the truncation with it, is gone.)
- **No word-boundary detection.** Breaks are character-level, matching what the
  terminal would do to the same text. The one exception is whitespace sitting
  exactly on a break: the space a line broke on is dropped from the continuation,
  where prose keeps it as a stray leading space.
- **No tunable threshold.** There is no minimum column measure, no minimum value
  measure and no starting column width to adjust. Three such constants existed
  during development and every one was deleted after measurement showed it either
  shadowed another gate or could not bind at all.
- **No styling.** The grid is plain text.

## Two known limits

**A row must start with `|`.** GFM also allows omitting the leading and trailing
pipe; this renderer does not. The restriction is load-bearing rather than
oversight: the streaming path decides whether a delta could touch a table by
testing for exactly that leading pipe (`deltaHasPipeLine`), so relaxing the parser
without relaxing that test would let real table rows take the incremental append
and render half-reflowed. `TestTableDetectionPredicatesAgree` fails if the two
predicates ever drift apart.

**Data can contain the border glyphs.** A grid drawn with characters has this
problem whenever the data may also contain those characters, and the content is
never altered to avoid it — substituting a different glyph would be editing the
model's text. The two cases are not symmetric:

An escaped `\|` is a literal `|` in the cell, and it now sits inside a frame made
of `│`, so the two are distinguishable. This actually *improved* with the grid:
under the previous ASCII framing the border was `|` too, so data and structure
were the same character.

<!-- @example src=pipes_ascii width=40 -->
```
┌─────┬─────┐
│ cmd │ out │
├─────┼─────┤
│ a|b │ ok  │
└─────┴─────┘
```

A cell containing `│` is the reverse — it is exactly the border glyph, so that row
shows four verticals where its neighbours show three and reads as an extra
column. Every width is still computed correctly; only the reading is ambiguous:

<!-- @example src=pipes_box width=40 -->
```
┌─────┬─────┐
│ cmd │ out │
├─────┼─────┤
│ a│b │ ok  │
└─────┴─────┘
```

Both cases are pinned by `TestRenderMarkdownTables_ContentGlyphsSurvive`, which
asserts the exact rendered rows. That test exists because the "nothing is lost"
multiset check *cannot* see this content: box glyphs are stripped as framing
noise on both sides, so a dropped `│` would pass it silently.

## Related

- Keybindings and window behaviour: [tui.md](tui.md)
- Streaming cost, allocation counts and why timings are not quoted:
  [internal/virtual-rendering-performance.md](internal/virtual-rendering-performance.md)
- How tool output is bounded before it ever reaches the renderer:
  [truncation.md](truncation.md)
