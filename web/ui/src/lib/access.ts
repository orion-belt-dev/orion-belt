// Access types relayed as raw TCP through the agent. Their grants name no
// remote user: the target authenticates the login itself.
export function isTunnel(accessType: string): boolean {
  return accessType === "rdp" || accessType === "vnc";
}
