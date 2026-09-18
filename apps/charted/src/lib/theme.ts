/**
 * Light and dark, remembered.
 *
 * The first paint is handled in index.html, before this module exists. This is
 * only the toggle and the write-back, so the two have to agree on one key and
 * one attribute -- `charted.theme` and `data-theme` on <html>.
 */

export type Theme = "light" | "dark";

export function currentTheme(): Theme {
  return document.documentElement.dataset.theme === "dark" ? "dark" : "light";
}

export function setTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  try {
    localStorage.setItem("charted.theme", theme);
  } catch {
    // A private window that refuses storage still gets the theme it clicked;
    // it just will not have it next time.
  }
}
