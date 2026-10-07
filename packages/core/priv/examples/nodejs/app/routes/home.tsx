import type { Route } from "./+types/home";
import { getUserFromSession } from "~/services/auth.server";
import { HomeView } from "~/views/public/home/home-view";

export function meta() {
  return [
    { title: "nvoi - React Router on Node" },
    { name: "description", content: "Welcome to nvoi!" },
  ];
}

export async function loader({ request }: Route.LoaderArgs) {
  const user = await getUserFromSession(request);
  return { user };
}

export default function Home({ loaderData }: Route.ComponentProps) {
  return <HomeView {...loaderData} />;
}
