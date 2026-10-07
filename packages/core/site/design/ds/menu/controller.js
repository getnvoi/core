import { Controller } from "@hotwired/stimulus"

// The panel is a manual popover in the top layer, placed from the trigger's box and scaled from its side.
// From a pointer it animates and the panel takes focus; from the keyboard it appears at once with the first
// (or picked) row focused. Rows hold focus while highlighted, so arrows, Enter and typeahead act on it.
export default class extends Controller {
  static targets = ["trigger", "panel", "row", "search", "none", "value", "shown", "toggle", "clearing"]
  static values = { align: String, side: String, submit: Boolean, placeholder: String }

  connect() {
    const panel = this.panelTarget
    panel.id ||= `ds-menu-${Math.random().toString(36).slice(2, 9)}`
    this.button?.setAttribute("aria-haspopup", "menu")
    this.button?.setAttribute("aria-expanded", "false")
    this.button?.setAttribute("aria-controls", panel.id)
    this.away = (event) => {
      if (this.isOpen && !this.triggerTarget.contains(event.target) && !panel.contains(event.target)) this.close({ restore: false })
    }
    this.moved = (event) => { if (this.isOpen && !panel.contains(event.target)) this.place() }
    document.addEventListener("pointerdown", this.away, true)
    panel.addEventListener("pointermove", (event) => this.hover(event))
    panel.addEventListener("pointerleave", () => this.rest())
  }

  disconnect() {
    document.removeEventListener("pointerdown", this.away, true)
    this.follow(false)
    if (this.isOpen) this.panelTarget.hidePopover()
  }

  // The panel follows its trigger through scrolls and resizes only while open, so a page of closed menus
  // adds no listener to every scroll.
  follow(on) {
    const method = on ? "addEventListener" : "removeEventListener"
    window[method]("scroll", this.moved, true)
    window[method]("resize", this.moved)
  }

  get button() { return this.triggerTarget.querySelector("button, a, [tabindex]") }
  get isOpen() { return this.panelTarget.matches(":popover-open") }
  get rows() { return this.rowTargets.filter((row) => !row.hidden) }

  // A click from Enter or Space has no pointer detail: that is the keyboard.
  toggle(event) {
    if (this.button?.disabled) return
    if (this.isOpen) this.close({ instant: event.detail === 0 })
    else this.open({ keyboard: event.detail === 0 })
  }

  press(event) {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return
    event.preventDefault()
    if (!this.isOpen) this.open({ keyboard: true, last: event.key === "ArrowUp" })
  }

  open({ keyboard = false, last = false } = {}) {
    const panel = this.panelTarget
    panel.classList.toggle("is-instant", keyboard)
    if (this.hasSearchTarget) { this.searchTarget.value = ""; this.filter() }
    panel.showPopover()
    this.follow(true)
    this.place()
    this.button?.setAttribute("aria-expanded", "true")
    if (this.hasSearchTarget) return this.searchTarget.focus({ preventScroll: true })
    if (!keyboard) return panel.focus({ preventScroll: true })
    const rows = this.rows
    const picked = rows.find((row) => row.getAttribute("aria-checked") === "true" && row.role === "menuitemradio")
    ;(picked || rows[last ? rows.length - 1 : 0])?.focus()
  }

  close({ instant = false, restore = true } = {}) {
    if (!this.isOpen) return
    const inside = this.panelTarget.contains(document.activeElement)
    this.panelTarget.classList.toggle("is-instant", instant)
    this.panelTarget.hidePopover()
    this.follow(false)
    this.button?.setAttribute("aria-expanded", "false")
    if (restore && inside) this.button?.focus({ preventScroll: true })
  }

  // Below the trigger, or above when only above fits; aligned to its start or end edge, kept 8px inside
  // the window. --origin puts the scale's anchor on the trigger.
  place() {
    const panel = this.panelTarget
    const anchor = this.button || this.element
    const box = anchor.getBoundingClientRect()
    if (this.hasShownTarget) panel.style.minWidth = `${box.width}px`
    const width = panel.offsetWidth
    const height = panel.offsetHeight
    const gap = 4
    const edge = 8
    const below = box.bottom + gap + height <= innerHeight - edge
    const above = box.top - gap - height >= edge
    const side = this.sideValue === "top" ? (above || !below ? "top" : "bottom") : (below || !above ? "bottom" : "top")
    const start = this.alignValue === "end" ? box.right - width : box.left
    const x = Math.min(Math.max(start, edge), innerWidth - width - edge)
    const y = side === "bottom" ? box.bottom + gap : box.top - gap - height
    panel.style.left = `${x}px`
    panel.style.top = `${Math.max(y, edge)}px`
    const originX = Math.min(Math.max((this.alignValue === "end" ? box.right : box.left) - x, 0), width)
    panel.style.setProperty("--origin", `${originX}px ${side === "bottom" ? "0" : "100%"}`)
  }

  key(event) {
    const key = event.key
    const inSearch = this.hasSearchTarget && event.target === this.searchTarget
    if (key === "ArrowDown" || key === "ArrowUp") {
      event.preventDefault()
      this.step(key === "ArrowDown" ? 1 : -1)
    } else if ((key === "Home" || key === "End") && !inSearch) {
      event.preventDefault()
      const rows = this.rows
      rows[key === "Home" ? 0 : rows.length - 1]?.focus()
    } else if (key === "Escape" || key === "Tab") {
      event.preventDefault()
      event.stopPropagation()
      this.close({ instant: true })
    } else if (key === "Enter" && inSearch) {
      event.preventDefault()
      this.rows[0]?.click()
    } else if (!this.hasSearchTarget && key.length === 1 && key !== " " && !event.metaKey && !event.ctrlKey && !event.altKey) {
      this.typeahead(key.toLowerCase())
    }
  }

  step(by) {
    const rows = this.rows
    if (!rows.length) return
    const at = rows.indexOf(document.activeElement)
    const next = at === -1 ? (by > 0 ? 0 : rows.length - 1) : (at + by + rows.length) % rows.length
    rows[next].focus()
  }

  // Letters typed within half a second build a prefix; one letter pressed again cycles through its rows.
  typeahead(letter) {
    this.typed = (this.typed || "") + letter
    clearTimeout(this.typing)
    this.typing = setTimeout(() => (this.typed = ""), 500)
    const rows = this.rows
    const at = rows.indexOf(document.activeElement)
    const repeated = [ ...this.typed ].every((c) => c === this.typed[0])
    const query = repeated ? this.typed[0] : this.typed
    const from = Math.max(repeated ? at + 1 : at, 0)
    const order = rows.slice(from).concat(rows.slice(0, from))
    order.find((row) => row.querySelector(".ds-menu-label").textContent.trim().toLowerCase().startsWith(query))?.focus()
  }

  hover(event) {
    const row = event.target.closest?.(".ds-menu-row")
    if (row && this.rowTargets.includes(row) && document.activeElement !== row) row.focus({ preventScroll: true })
  }

  rest() {
    if (!this.panelTarget.contains(document.activeElement) || !this.isOpen) return
    ;(this.hasSearchTarget ? this.searchTarget : this.panelTarget).focus({ preventScroll: true })
  }

  filter() {
    const query = this.searchTarget.value.trim().toLowerCase()
    const panel = this.panelTarget
    let any = false
    panel.querySelectorAll(".ds-menu-row:not(.is-clear)").forEach((row) => {
      const hit = !query || row.textContent.toLowerCase().includes(query)
      row.hidden = !hit
      any ||= hit
    })
    panel.querySelectorAll(".ds-menu-group").forEach((group) => (group.hidden = !group.querySelector(".ds-menu-row:not([hidden])")))
    panel.querySelectorAll(".ds-menu-rule:not(.is-clear)").forEach((rule) => (rule.hidden = !!query))
    if (this.hasNoneTarget) this.noneTarget.hidden = any
    if (this.isOpen) this.place()
  }

  pick(event) {
    const row = event.currentTarget
    const value = row.dataset.value
    this.rowTargets.forEach((r) => { if (r.role === "menuitemradio") r.setAttribute("aria-checked", String(r === row)) })
    if (this.hasShownTarget) this.shownTarget.textContent = row.querySelector(".ds-menu-label").textContent
    if (this.hasValueTarget) {
      this.valueTarget.value = value
      this.valueTarget.dispatchEvent(new Event("change", { bubbles: true }))
    }
    this.clearingTargets.forEach((part) => (part.hidden = value === ""))
    this.close({ instant: event.detail === 0 })
    this.dispatch("pick", { detail: { value } })
    if (this.submitValue) this.element.closest("form")?.requestSubmit()
  }

  // The clear row empties the select and puts the placeholder back on the trigger.
  reset(event) {
    this.rowTargets.forEach((r) => { if (r.role === "menuitemradio") r.setAttribute("aria-checked", "false") })
    if (this.hasShownTarget) this.shownTarget.textContent = this.placeholderValue
    if (this.hasValueTarget) {
      this.valueTarget.value = ""
      this.valueTarget.dispatchEvent(new Event("change", { bubbles: true }))
    }
    this.clearingTargets.forEach((part) => (part.hidden = true))
    this.close({ instant: event.detail === 0 })
    this.dispatch("pick", { detail: { value: "" } })
    if (this.submitValue) this.element.closest("form")?.requestSubmit()
  }

  // A switch row flips and the menu stays open, so the next one is a click away.
  flip(event) {
    const row = event.currentTarget
    const on = row.getAttribute("aria-checked") !== "true"
    row.setAttribute("aria-checked", String(on))
    const input = this.toggleTargets.find((i) => i.name === row.dataset.toggle)
    // Off is blank, so a GET form drops it and the URL holds only what narrows.
    if (input) input.value = on ? "1" : ""
    this.dispatch("flip", { detail: { name: row.dataset.toggle, on } })
    if (this.submitValue) this.element.closest("form")?.requestSubmit()
  }

  act(event) {
    this.close({ instant: event.detail === 0 })
  }
}
