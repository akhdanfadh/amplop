import { useLayoutEffect, useState } from "react";

// useOverflow reports whether an element's text is cut short, e.g. by an
// ellipsis, so the full text can be offered elsewhere. It returns a ref
// callback for the element and whether its content is wider than it.
// text is what the element shows: a change measures again, as a resize
// does.
export function useOverflow(text: string): [(element: HTMLElement | null) => void, boolean] {
  const [element, setElement] = useState<HTMLElement | null>(null);
  const [cut, setCut] = useState(false);
  useLayoutEffect(() => {
    if (!element) return undefined;
    const measure = () => setCut(element.scrollWidth > element.clientWidth);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [element, text]);
  return [setElement, cut];
}
