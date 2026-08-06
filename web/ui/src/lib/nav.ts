import type { Role } from "./types";
import type { IconComponent } from "../components/icons";
import {
  DashboardIcon,
  SetupIcon,
  CatalogIcon,
  RequestsIcon,
  MachinesIcon,
  TerminalIcon,
  FilesIcon,
  SessionsIcon,
  UsersIcon,
  PermissionsIcon,
  AgentsIcon,
  AddAgentIcon,
  AuditIcon,
  SecurityIcon,
  PluginsIcon,
} from "../components/icons";

export function roleOf(user: { role?: string; is_admin?: boolean } | null | undefined): Role {
  if (!user) return "user";
  const r = user.role || "";
  if (r === "admin" || r === "operator" || r === "auditor") return r;
  if (user.is_admin) return "admin";
  if (r === "user") return "user";
  return (r as Role) || "user";
}

export function canApprove(user: { role?: string; is_admin?: boolean } | null | undefined): boolean {
  const r = roleOf(user);
  return r === "admin" || r === "operator";
}

export type NavItem = { id: string; label: string; icon: IconComponent; path: string };

const BASE: NavItem[] = [
  { id: "dashboard", label: "Dashboard", icon: DashboardIcon, path: "/" },
  { id: "setup", label: "Setup guide", icon: SetupIcon, path: "/setup" },
  { id: "catalog", label: "Access catalog", icon: CatalogIcon, path: "/catalog" },
  { id: "requests", label: "Access requests", icon: RequestsIcon, path: "/requests" },
  { id: "machines", label: "Machines", icon: MachinesIcon, path: "/machines" },
  { id: "terminal", label: "Terminal", icon: TerminalIcon, path: "/terminal" },
  { id: "files", label: "Files", icon: FilesIcon, path: "/files" },
  { id: "sessions", label: "Sessions", icon: SessionsIcon, path: "/sessions" },
  { id: "users", label: "Users", icon: UsersIcon, path: "/users" },
  { id: "permissions", label: "Permissions", icon: PermissionsIcon, path: "/permissions" },
  { id: "agents", label: "Agents", icon: AgentsIcon, path: "/agents" },
  { id: "add-agent", label: "Add agent", icon: AddAgentIcon, path: "/add-agent" },
  { id: "audit", label: "Audit", icon: AuditIcon, path: "/audit" },
  { id: "security", label: "Security", icon: SecurityIcon, path: "/security" },
  { id: "plugins", label: "Plugins", icon: PluginsIcon, path: "/plugins" },
];

function pick(...ids: string[]): NavItem[] {
  return ids.map((id) => BASE.find((n) => n.id === id)!).filter(Boolean);
}

export const NAV: Record<Role, NavItem[]> = {
  admin: BASE,
  operator: BASE,
  auditor: pick("dashboard", "sessions", "users", "audit", "security"),
  // The catalog leads for a plain user: it is where they find out what they
  // can ask for, instead of guessing machine names on the request form.
  user: pick("catalog", "machines", "terminal", "files", "sessions", "requests", "audit", "security"),
};

export function defaultPathForRole(role: Role): string {
  return (NAV[role] || NAV.user)[0]?.path || "/";
}
