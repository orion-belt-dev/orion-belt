// Access types relayed as raw TCP through the agent. Their grants name no
// remote user: the target authenticates the login itself.
export function isTunnel(accessType: string): boolean {
  return accessType === "rdp" || accessType === "vnc";
}

// Label and placeholder for the remote-user field, which holds
// namespace/role scopes for k8s grants.
export function targetField(accessType: string, plural: boolean): { label: string; placeholder?: string } {
  if (accessType === "k8s") {
    return {
      label: plural ? "Scopes (namespace/role, comma-separated)" : "Scope (namespace/role)",
      placeholder: "payments/edit or */view",
    };
  }
  if (isTunnel(accessType)) return { label: plural ? "Remote users" : "Remote user", placeholder: "Not used for rdp/vnc" };
  return { label: plural ? "Remote users (comma-separated)" : "Remote user", placeholder: plural ? "root,ubuntu" : undefined };
}
