import {
  useEffect,
  useEffectEvent,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactElement,
  type RefObject,
} from "react";
import { useIsFetching } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { SearchIcon, XIcon } from "lucide-react";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group";
import { Spinner } from "@/components/ui/spinner";
import { searchPlace, type Place } from "./places";
import { searchResultsKey, words, type SearchFilters } from "./search";
import { clearSearch, resultsLoading, searchName } from "./text";

// How long typing rests before the box searches (DESIGN.md "Search").
export const searchAfterMs = 200;

// SearchBox is the "Search mail" box: value is its text, placeholder the
// places it searches. Typing calls onChange; Enter, onEnter; Escape and
// its clear button, onLeave; focus coming and going, onFocusChange. A
// spinner shows while a search runs.
export function SearchBox({
  inputRef,
  value,
  placeholder,
  onChange,
  onEnter,
  onLeave,
  onFocusChange,
  className,
}: {
  inputRef: RefObject<HTMLInputElement | null>;
  value: string;
  placeholder: string;
  onChange: (value: string) => void;
  onEnter: () => void;
  onLeave: () => void;
  onFocusChange?: (focused: boolean) => void;
  className?: string;
}): ReactElement {
  const running = useIsFetching({ queryKey: searchResultsKey }) > 0;
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") {
      event.preventDefault();
      onEnter();
    } else if (event.key === "Escape") {
      event.preventDefault();
      onLeave();
    }
  };
  return (
    <InputGroup className={className}>
      <InputGroupAddon>
        <SearchIcon aria-hidden="true" />
      </InputGroupAddon>
      <InputGroupInput
        ref={inputRef}
        type="search"
        enterKeyHint="search"
        autoComplete="off"
        spellCheck={false}
        aria-label={searchName}
        placeholder={placeholder}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={onKeyDown}
        onFocus={() => onFocusChange?.(true)}
        onBlur={() => onFocusChange?.(false)}
        // The browser's own clear button would clear without leaving the
        // search; the box has its own.
        className="[&::-webkit-search-cancel-button]:appearance-none"
      />
      {(running || value !== "") && (
        <InputGroupAddon align="inline-end">
          {running && (
            <>
              <Spinner />
              <span className="sr-only">{resultsLoading}</span>
            </>
          )}
          {value !== "" && (
            <InputGroupButton size="icon-sm" aria-label={clearSearch} onPress={onLeave}>
              <XIcon aria-hidden="true" />
            </InputGroupButton>
          )}
        </InputGroupAddon>
      )}
    </InputGroup>
  );
}

// SearchBoxState is what useSearchBox gives the box and the page around
// it.
export type SearchBoxState = {
  text: string;
  onChange: (value: string) => void;
  onEnter: () => void;
  onLeave: () => void;
};

// inheritedPlaces returns the places a search started in place searches:
// that place, except from the Inbox or All mail, which search all mail
// (DESIGN.md "Search").
export function inheritedPlaces(place: Place): string[] | undefined {
  return place === "inbox" || place === "all" ? undefined : [place];
}

// useSearchBox runs the search box on a place's page, place, of account
// alone when given, and search, the open search on /mail/search. Words
// of 2 characters or more, once typing rests 200 ms or at Enter, open the
// search's results: from a place, a new page searching that place
// (inheritedPlaces); on the results, the same page with the new words,
// replacing it in the history, so Back leaves the search rather than
// stepping through each word. Emptying the box searches the filters alone.
// onLeave, from Escape or the clear button, returns to the place the
// search started from, or the one place it searches, or the Inbox, and
// runs onLeft; on a place's own page it empties the box.
export function useSearchBox({
  place,
  account,
  search,
  onLeft,
}: {
  place: Place;
  account: number | undefined;
  search: SearchFilters | undefined;
  onLeft?: () => void;
}): SearchBoxState {
  const navigate = useNavigate();
  const [text, setText] = useState(search?.q ?? "");
  // The place the search started from, while its page lives.
  const origin = useRef<Place | undefined>(undefined);
  // Typing not yet searched, and its timer.
  const typed = useRef(false);
  const timer = useRef<number | undefined>(undefined);
  // Whether the box itself is moving the address, which then keeps what
  // the member types meanwhile.
  const moving = useRef(false);

  // The page moved another way, e.g. a sidebar press or Back to an earlier
  // search: typing not yet searched was for the page before, so it is
  // dropped, and the box shows the new page's words.
  const at = `${place} ${account ?? ""} ${JSON.stringify(search ?? null)}`;
  const q = search?.q;
  useEffect(() => {
    if (moving.current) {
      moving.current = false;
      return;
    }
    window.clearTimeout(timer.current);
    typed.current = false;
    setText(q ?? "");
  }, [at, q]);

  const go = (value: string) => {
    typed.current = false;
    window.clearTimeout(timer.current);
    const found = words(value);
    if (search) {
      // Words too short to search keep the last results.
      if (found === search.q || (found === undefined && value.trim() !== "")) return;
      moving.current = true;
      void navigate({
        to: "/mail/$place",
        params: { place: searchPlace },
        search: { ...search, account, q: found },
        replace: true,
      });
      return;
    }
    if (found === undefined) return;
    origin.current = place;
    moving.current = true;
    void navigate({
      to: "/mail/$place",
      params: { place: searchPlace },
      search: { account, q: found, place: inheritedPlaces(place) },
    });
  };

  const searchTyped = useEffectEvent(() => go(text));
  useEffect(() => {
    if (!typed.current) return undefined;
    timer.current = window.setTimeout(searchTyped, searchAfterMs);
    return () => window.clearTimeout(timer.current);
  }, [text]);

  return {
    text,
    onChange: (value) => {
      typed.current = true;
      setText(value);
    },
    onEnter: () => go(text),
    onLeave: () => {
      typed.current = false;
      window.clearTimeout(timer.current);
      setText("");
      if (search) {
        const back = origin.current ?? (search.place?.length === 1 ? search.place[0] : "inbox");
        origin.current = undefined;
        void navigate({ to: "/mail/$place", params: { place: back }, search: { account } });
      }
      onLeft?.();
    },
  };
}
