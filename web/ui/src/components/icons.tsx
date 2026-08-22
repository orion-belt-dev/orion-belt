import type { SVGProps } from "react";

// Nav iconography: thin outline, rounded joins, no fills — color and glow
// are controlled entirely by CSS (currentColor + drop-shadow on .active).
type IconProps = SVGProps<SVGSVGElement>;

function Icon({ children, ...props }: IconProps & { children: React.ReactNode }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.6}
      strokeLinecap="round"
      strokeLinejoin="round"
      {...props}
    >
      {children}
    </svg>
  );
}

export function DashboardIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <rect x="3.5" y="3.5" width="7.5" height="7.5" rx="1.6" />
      <rect x="13" y="3.5" width="7.5" height="4.5" rx="1.6" />
      <rect x="13" y="10.5" width="7.5" height="10" rx="1.6" />
      <rect x="3.5" y="13.5" width="7.5" height="7" rx="1.6" />
    </Icon>
  );
}

export function SetupIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <rect x="4" y="3" width="16" height="18" rx="2" />
      <path d="M8 8.5l1.3 1.3L11.5 7.5" />
      <line x1="13.5" y1="8.2" x2="17" y2="8.2" />
      <line x1="8" y1="13.5" x2="17" y2="13.5" />
      <line x1="8" y1="17" x2="14" y2="17" />
    </Icon>
  );
}

export function RequestsIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M4 13V6a1.5 1.5 0 0 1 1.5-1.5h13A1.5 1.5 0 0 1 20 6v7" />
      <path d="M4 13l3.2 4.3a2 2 0 0 0 1.6.8h6.4a2 2 0 0 0 1.6-.8L20 13" />
      <path d="M4 13h4.2l1 1.6h5.6l1-1.6H20" />
    </Icon>
  );
}

export function CatalogIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <rect x="3.5" y="4.5" width="7" height="7" rx="1.4" />
      <rect x="13.5" y="4.5" width="7" height="7" rx="1.4" />
      <rect x="3.5" y="14" width="7" height="6" rx="1.4" />
      <circle cx="17" cy="17" r="3.2" />
      <line x1="17" y1="15.4" x2="17" y2="18.6" />
      <line x1="15.4" y1="17" x2="18.6" y2="17" />
    </Icon>
  );
}

export function MachinesIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <rect x="3.5" y="4" width="17" height="5" rx="1.4" />
      <rect x="3.5" y="10.5" width="17" height="5" rx="1.4" />
      <rect x="3.5" y="17" width="17" height="3.2" rx="1.2" />
      <circle cx="7" cy="6.5" r="0.6" fill="currentColor" stroke="none" />
      <circle cx="7" cy="13" r="0.6" fill="currentColor" stroke="none" />
    </Icon>
  );
}

export function TerminalIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
      <path d="M7.5 9.5l3 2.7-3 2.7" />
      <line x1="12.5" y1="15" x2="16.5" y2="15" />
    </Icon>
  );
}

export function FilesIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M3.75 6.25A1.75 1.75 0 0 1 5.5 4.5h4l1.75 2h7A1.75 1.75 0 0 1 20 8.25v9.5A1.75 1.75 0 0 1 18.25 19.5H5.5a1.75 1.75 0 0 1-1.75-1.75z" />
    </Icon>
  );
}

export function SessionsIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <rect x="3.5" y="4.5" width="17" height="12" rx="1.8" />
      <path d="M10.5 8.5l4.5 3-4.5 3z" />
      <line x1="9" y1="20" x2="15" y2="20" />
      <line x1="12" y1="16.5" x2="12" y2="20" />
    </Icon>
  );
}

export function UsersIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <circle cx="9.5" cy="8" r="3" />
      <path d="M4 19c0-3 2.5-5 5.5-5s5.5 2 5.5 5" />
      <circle cx="16.5" cy="8.5" r="2.3" />
      <path d="M15.5 11.5c2.3.3 4 2 4.3 4.2" />
    </Icon>
  );
}

export function PermissionsIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <circle cx="8" cy="15.5" r="3.2" />
      <path d="M10.3 13.2 17 6.5" />
      <path d="M14.5 9 17 6.5l2 2" />
      <path d="M12.7 10.8 14.5 12.6" />
    </Icon>
  );
}

export function AgentsIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <rect x="7" y="7" width="10" height="10" rx="1.6" />
      <rect x="10" y="10" width="4" height="4" rx="0.8" />
      <line x1="12" y1="3" x2="12" y2="6" />
      <line x1="12" y1="18" x2="12" y2="21" />
      <line x1="3" y1="12" x2="6" y2="12" />
      <line x1="18" y1="12" x2="21" y2="12" />
    </Icon>
  );
}

export function AddAgentIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <rect x="4.5" y="7" width="9" height="10" rx="1.6" />
      <rect x="7.2" y="9.7" width="3.6" height="3.6" rx="0.7" />
      <line x1="9" y1="3.2" x2="9" y2="6" />
      <line x1="9" y1="18" x2="9" y2="20.8" />
      <line x1="17.5" y1="9" x2="17.5" y2="15" />
      <line x1="14.5" y1="12" x2="20.5" y2="12" />
    </Icon>
  );
}

export function AuditIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <rect x="5.5" y="4" width="13" height="17" rx="1.8" />
      <rect x="9" y="2.7" width="6" height="3" rx="1" />
      <path d="M8.5 12l2 2 4.5-4.5" />
      <line x1="8.5" y1="17" x2="15.5" y2="17" />
    </Icon>
  );
}

export function SecurityIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M12 3.2 5 6v5.5c0 4.4 2.9 8 7 9.3 4.1-1.3 7-4.9 7-9.3V6z" />
      <path d="M9.2 12l1.9 1.9 3.7-3.9" />
    </Icon>
  );
}

export function PluginsIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M9 4.5h3v2a1.5 1.5 0 0 0 3 0v-2h3a1.2 1.2 0 0 1 1.2 1.2v3h-2a1.5 1.5 0 0 0 0 3h2v3A1.2 1.2 0 0 1 18 15.7h-3v-2a1.5 1.5 0 0 0-3 0v2H9a1.2 1.2 0 0 1-1.2-1.2v-3h2a1.5 1.5 0 0 0 0-3h-2v-3A1.2 1.2 0 0 1 9 4.5z" />
    </Icon>
  );
}

export function PanelCollapseIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <rect x="3.5" y="4" width="17" height="16" rx="2" />
      <path d="M9.5 4v16" />
      <path d="M14.5 9.5l-3 2.5 3 2.5" />
    </Icon>
  );
}

export function PanelExpandIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <rect x="3.5" y="4" width="17" height="16" rx="2" />
      <path d="M9.5 4v16" />
      <path d="M12 9.5l3 2.5-3 2.5" />
    </Icon>
  );
}

export function LogoutIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M10 4.5H6.5A2 2 0 0 0 4.5 6.5v11A2 2 0 0 0 6.5 19.5H10" />
      <path d="M14 12H20" />
      <path d="M17 9l3 3-3 3" />
    </Icon>
  );
}

export type IconComponent = (props: IconProps) => React.JSX.Element;
