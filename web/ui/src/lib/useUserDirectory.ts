import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuth } from "../auth/AuthContext";
import { api } from "./api";
import { roleOf } from "./nav";
import type { User } from "./types";

// useUserDirectory resolves user IDs to names for list pages. Only admins,
// operators and auditors may read the full directory; a plain user sees
// their own records only, so their own account is all they need and the
// directory is never requested for them.
export function useUserDirectory(): { data: User[] | undefined } {
  const { user } = useAuth();
  const role = roleOf(user);
  const privileged = role === "admin" || role === "operator" || role === "auditor";
  const q = useQuery({
    queryKey: ["users"],
    queryFn: () => api<User[]>("/users"),
    enabled: privileged,
  });
  const self = useMemo(() => (user ? [user] : []), [user]);
  return { data: privileged ? q.data : self };
}
