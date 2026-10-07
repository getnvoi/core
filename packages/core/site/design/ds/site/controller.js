import { Controller } from "@hotwired/stimulus"

// Light or dark for public pages, kept per browser.
export default class extends Controller {
  static targets = ["theme"]

  connect() {
    try { if (localStorage.publicDark === "1") document.documentElement.classList.add("dark") } catch {}
    this.label()
  }

  theme() {
    const on = document.documentElement.classList.toggle("dark")
    try { localStorage.publicDark = on ? "1" : "0" } catch {}
    this.label()
  }

  label() {
    if (this.hasThemeTarget) this.themeTarget.textContent = document.documentElement.classList.contains("dark") ? "Light" : "Dark"
  }
}
