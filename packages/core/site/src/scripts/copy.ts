// ds/code's copy button, as its controller's copy() does it: the body's text without the trailing newline,
// then is-done for 1500ms. The blocks are built at build time, so no controller is attached.
document.addEventListener("click", async (event) => {
  const button = (event.target as Element).closest?.<HTMLElement>(".ds-code-copy");
  const body = button?.closest(".ds-code")?.querySelector<HTMLElement>(".ds-code-body");
  if (!button || !body) return;
  await navigator.clipboard.writeText(body.innerText.replace(/\n$/, ""));
  button.classList.add("is-done");
  clearTimeout(Number(button.dataset.timer));
  button.dataset.timer = String(setTimeout(() => button.classList.remove("is-done"), 1500));
});
