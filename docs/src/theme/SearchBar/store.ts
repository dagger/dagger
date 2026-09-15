// Shared open/close state for the search palette, plus the single global
// Cmd/Ctrl+K (and Escape) shortcut.
//
// SearchBar renders in two places — the navbar and the docs sidebar — so there
// are two trigger buttons on docs pages. Both talk to this one store, and a
// single <SearchPalette> (mounted once at @theme/Root) renders the overlay, so
// the palette can never open twice.

let open = false;
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

export function getOpen(): boolean {
  return open;
}

export function setOpen(next: boolean): void {
  if (open === next) return;
  open = next;
  emit();
}

export function subscribe(onChange: () => void): () => void {
  listeners.add(onChange);
  return () => listeners.delete(onChange);
}

// Install the global shortcut once. Module side effects run a single time no
// matter how many SearchBar triggers mount, so there is only ever one listener.
if (typeof document !== "undefined") {
  document.addEventListener("keydown", (ev) => {
    if ((ev.ctrlKey || ev.metaKey) && (ev.key === "k" || ev.key === "K")) {
      ev.preventDefault();
      setOpen(!open);
    } else if (ev.key === "Escape" && open) {
      setOpen(false);
    }
  });
}
