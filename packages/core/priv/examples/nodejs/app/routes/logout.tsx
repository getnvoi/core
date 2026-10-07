import type { Route } from "./+types/logout";

export async function action({ request }: Route.ActionArgs) {
  const { logout } = await import("~/services/auth.server");
  return logout(request);
}

export async function loader() {
  // Logout should only be done via POST (action), not GET (loader)
  // This prevents CSRF attacks
  return new Response("Method not allowed", { status: 405 });
}

export default function Logout() {
  return null;
}
