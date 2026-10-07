import { useOutletContext } from "react-router";
import type { SessionUser } from "~/sessions.server";
import { DashboardView } from "~/views/app/dashboard/dashboard-view";

export default function AppDashboard() {
  const { user } = useOutletContext<{ user: SessionUser }>();
  return <DashboardView user={user} />;
}
