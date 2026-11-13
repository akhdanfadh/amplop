// The events a mouse sends, for pointTo and pointAway.
const mouse = { bubbles: true, pointerType: "mouse", pointerId: 1, isPrimary: true };

// pointTo moves a mouse onto element at once, inside the page. Playwright's
// own hover is a round trip, during which a test's fake clock keeps moving,
// further the busier the machine.
export function pointTo(element: Element): void {
  element.dispatchEvent(new PointerEvent("pointerover", mouse));
  element.dispatchEvent(new PointerEvent("pointerenter", { ...mouse, bubbles: false }));
  element.dispatchEvent(new PointerEvent("pointermove", mouse));
}

// pointAway moves the mouse off element at once, as pointTo moves it on.
export function pointAway(element: Element): void {
  element.dispatchEvent(new PointerEvent("pointerout", mouse));
  element.dispatchEvent(new PointerEvent("pointerleave", { ...mouse, bubbles: false }));
}
