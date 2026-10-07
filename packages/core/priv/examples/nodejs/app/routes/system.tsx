import { SystemView } from "~/views/system/dashboard/system-view";

export function meta() {
  return [
    { title: "System Dashboard - nvoi" },
    { name: "description", content: "Internal system dashboard" },
  ];
}

export default function SystemDashboard() {
  return <SystemView />;
}
