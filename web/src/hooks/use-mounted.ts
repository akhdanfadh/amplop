import { useCallback, useEffect, useRef } from "react";

// useMounted returns a function that reports whether the component is still
// on the page, for work that resumes after an await, e.g. a navigation once a
// request answers.
export function useMounted(): () => boolean {
  const mounted = useRef(false);

  // Set in an effect, not at render: Strict Mode unmounts and mounts again,
  // and the second mount must count.
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  return useCallback(() => mounted.current, []);
}
