import type { ReactElement } from "react";
import { createFileRoute, notFound } from "@tanstack/react-router";
import { PlacePage } from "@/features/mail/place-page";
import { isViewPlace } from "@/features/mail/places";
import { placeName } from "@/features/mail/text";

// A place's mail, /mail/<place>: its conversation list.
export const Route = createFileRoute("/_app/_mail/mail/$place")({
  params: {
    parse: ({ place }) => {
      if (!isViewPlace(place)) throw notFound();
      return { place };
    },
  },
  component: Place,
  // head also runs for an address that is no place, which shows Page not
  // found and names itself.
  head: ({ params }) =>
    isViewPlace(params.place) ? { meta: [{ title: `${placeName[params.place]} - inbok` }] } : {},
});

function Place(): ReactElement {
  const { place } = Route.useParams();
  return <PlacePage place={place} />;
}
