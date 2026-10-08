import { Container, Editor, Input, SelectList, Text, fuzzyFilter } from '@earendil-works/pi-tui';
import { plain } from './conversation-ui.mjs';
import { selectTheme } from './presentation.mjs';

/** Public Editor composition; application actions leave model/storage ownership in durable. */
export class ActionEditor extends Editor {
  actions = new Map();
  constructor(tui, theme, keybindings, options) { super(tui, theme, options); this.keybindings = keybindings; }
  onAction(name, handler) { this.actions.set(name, handler); }
  handleInput(data) {
    // Native 0.87.1 special actions precede explicit history and generic actions.
    if (this.keybindings.matches(data, 'app.interrupt')) {
      const handler = this.actions.get('app.interrupt');
      if (!this.isShowingAutocomplete() && handler) { handler(); return; }
      return super.handleInput(data); // Parent owns autocomplete cancellation.
    }
    if (this.keybindings.matches(data, 'app.exit') && this.getText().length === 0) {
      this.actions.get('app.exit')?.(); return;
    }
    if (this.keybindings.matches(data, 'tui.editor.historyPrevious') || this.keybindings.matches(data, 'tui.editor.historyNext')) return super.handleInput(data);
    for (const [name, handler] of this.actions) if (name !== 'app.interrupt' && name !== 'app.exit' && this.keybindings.matches(data, name)) {
      handler(); return;
    }
    super.handleInput(data);
  }
}

/** One interaction/one focused search input. No native ModelRuntime or session facade. */
export class SearchSelector extends Container {
  pageSize = 12;
  constructor(title, items, selected, theme, keybindings, finish) {
    super(); this.keybindings = keybindings; this.items = items; this.theme = theme; this.finish = finish;
    this.addChild(new Text(theme.fg('accent', plain(title)), 0, 0));
    this.search = new Input({ prompt: 'Search: ' }); this.addChild(this.search);
    this.listContainer = new Container(); this.addChild(this.listContainer); this.filter('');
    this.list.setSelectedIndex(Math.max(0, items.findIndex(item => item.value === selected)));
    this.addChild(new Text(theme.fg('dim', '↑↓ select · page up/down · Enter confirm · Escape cancel'), 0, 0));
  }
  filter(query) {
    this.filteredItems = fuzzyFilter(this.items, query, item => `${item.value} ${item.label} ${item.description || ''}`);
    this.list = new SelectList(this.filteredItems, this.pageSize, selectTheme(this.theme));
    this.list.onSelect = item => this.finish(item.value); this.list.onCancel = () => this.finish();
    this.listContainer.clear(); this.listContainer.addChild(this.list);
  }
  get focused() { return this.search.focused; }
  set focused(value) { this.search.focused = value; }
  handleInput(data) {
    if (['tui.select.up', 'tui.select.down'].some(name => this.keybindings.matches(data, name))) this.list.handleInput(data);
    else if (this.keybindings.matches(data, 'tui.select.pageUp') || this.keybindings.matches(data, 'tui.select.pageDown')) {
      // Pinned public SelectList has no paging handler; use only its public selection API.
      const index = this.filteredItems.indexOf(this.list.getSelectedItem());
      if (index < 0) return;
      const direction = this.keybindings.matches(data, 'tui.select.pageUp') ? -1 : 1;
      this.list.setSelectedIndex(Math.max(0, Math.min(this.filteredItems.length - 1, index + direction * this.pageSize)));
    } else if (['tui.select.confirm', 'tui.select.cancel'].some(name => this.keybindings.matches(data, name))) this.list.handleInput(data);
    else { this.search.handleInput(data); this.filter(this.search.getValue()); }
  }
}
