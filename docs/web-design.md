# Web design

The browser UI adopts Jeff Tao's personal design system v0.1, supplied in
`jeff-tao-design-system-v0.1.zip`. The app layer of that system governs the
workspace; its document typography governs rendered conversations.

The default is light, with the system's blue accent: white paper, black ink,
1px panel boundaries, square controls, solid selections, and hard ink offsets
for floating menus and dialogs. The reading pane uses `canvas = #F8F6F1` behind the paper conversation cards,
an Ostraka override of the supplied canvas token. Linked retained traces
are collapsible sections inside their agent reply card; traces without a
matching reply stay separate in the timeline. Desktop controls are 32px; phone controls are
44px with bottom navigation and the turn action within thumb reach. Item
properties are disclosed separately from the conversation, initially open on
desktop and collapsed on phones. Disclosure state survives live updates.

`internal/webui/static/tokens.css` carries the supplied colour, spacing, size,
and font tokens. It omits the upstream typography utility classes to avoid
colliding with existing diff selectors. `style.css` applies those tokens to
Ostraka's HTML components. No React runtime, CDN or frontend build is
required. The supplied Lato and JetBrains Mono WOFF2 files are bundled under
`static/fonts/` with their SIL Open Font Licenses. The candidate IBM Plex face
is deliberately omitted. Navigation uses six Lucide icons from the supplied
component bundle, with the system's square caps and mitred joins; its license
is preserved in `static/fonts/NOTICE.txt`.

Accent denotes selection or the primary action. Status swatches use the
system's categorical tags with text labels. Added/deleted diff branches use
blue/orange, with source signs retained; selected lines have an accent outline.
Code uses the `ink` scheme: bold keywords, muted italic comments, blue literals.
Markdown links are underlined, strong text uses solid blue highlights, and
emphasis uses upright accent text. In the composer overlay, font metrics and
padding remain identical to the native textarea so caret, selection, undo and
source characters stay aligned; highlights change colour without changing
text width. Errors carry an exclamation mark as well as their message.

The Ostraka name and document favicon remain Ostraka's identity, redrawn in
the system's geometry and palette. The personal JT favicon is not reused as
Ostraka's logo. Dark tokens are retained from upstream for future use, but this
implementation defaults to the settled light palette.

Dropdowns share the custom component in `static/select.js`: the supplied Select
field frame and square chevron combined with the Menu's ink border, hard
shadow and blue active row. Native selects back form submission and existing
change handlers; their options, disabled state and programmatic values stay in
sync with the visible control. The component exposes combobox/listbox semantics,
arrow keys, Home/End, type-to-jump, Enter/Space selection, and Escape/Tab
cancellation. Menus use the browser popover layer to escape clipping and dialog
boundaries, flip upwards near the viewport edge, and use 44px phone rows.
`npm run select --prefix internal/webui/tests` checks the component against
`OSTRAKA_TEST_ORIGIN`, without writing project data. The main browser suite
also selects filters, models and review targets through the visible component.
