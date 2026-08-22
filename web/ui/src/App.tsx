import { Navigate, Route, Routes } from "react-router-dom";
import { useAuth, useHomePath, useRole } from "./auth/AuthContext";
import { AppShell } from "./components/AppShell";
import { LoginPage } from "./pages/LoginPage";
import { SetPasswordGate } from "./pages/SetPasswordGate";
import { DashboardPage } from "./pages/DashboardPage";
import { SetupPage } from "./pages/SetupPage";
import { SessionsPage } from "./pages/SessionsPage";
import { AddAgentPage } from "./pages/AddAgentPage";
import { TerminalPage } from "./pages/TerminalPage";
import { MachinesPage } from "./pages/MachinesPage";
import { AgentsPage } from "./pages/AgentsPage";
import { CatalogPage } from "./pages/CatalogPage";
import { RequestsPage } from "./pages/RequestsPage";
import { UsersPage } from "./pages/UsersPage";
import { AuditPage } from "./pages/AuditPage";
import { FilesPage } from "./pages/FilesPage";
import { SecurityPage } from "./pages/SecurityPage";
import { PermissionsPage } from "./pages/PermissionsPage";
import { PluginsPage } from "./pages/PluginsPage";
import { NAV } from "./lib/nav";

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, ready } = useAuth();
  if (!ready) return <p className="muted" style={{ padding: "2rem" }}>Loading…</p>;
  if (!user) return <Navigate to="/login" replace />;
  if (user.must_set_password) return <SetPasswordGate />;
  return children;
}

function RoleGate({ id, children }: { id: string; children: React.ReactNode }) {
  const role = useRole();
  const allowed = (NAV[role] || NAV.user).some((n) => n.id === id);
  const home = useHomePath();
  if (!allowed) return <Navigate to={home} replace />;
  return children;
}

function HomeRedirect() {
  const role = useRole();
  const home = useHomePath();
  if (role === "admin" || role === "operator" || role === "auditor") {
    return (
      <RoleGate id="dashboard">
        <DashboardPage />
      </RoleGate>
    );
  }
  return <Navigate to={home} replace />;
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/bootstrap" element={<LoginPage />} />
      <Route
        element={
          <RequireAuth>
            <AppShell />
          </RequireAuth>
        }
      >
        <Route index element={<HomeRedirect />} />
        <Route
          path="dashboard"
          element={
            <RoleGate id="dashboard">
              <DashboardPage />
            </RoleGate>
          }
        />
        <Route
          path="setup"
          element={
            <RoleGate id="setup">
              <SetupPage />
            </RoleGate>
          }
        />
        <Route
          path="catalog"
          element={
            <RoleGate id="catalog">
              <CatalogPage />
            </RoleGate>
          }
        />
        <Route
          path="requests"
          element={
            <RoleGate id="requests">
              <RequestsPage />
            </RoleGate>
          }
        />
        <Route
          path="machines"
          element={
            <RoleGate id="machines">
              <MachinesPage />
            </RoleGate>
          }
        />
        <Route
          path="terminal"
          element={
            <RoleGate id="terminal">
              <TerminalPage />
            </RoleGate>
          }
        />
        <Route
          path="files"
          element={
            <RoleGate id="files">
              <FilesPage />
            </RoleGate>
          }
        />
        <Route
          path="sessions"
          element={
            <RoleGate id="sessions">
              <SessionsPage />
            </RoleGate>
          }
        />
        <Route
          path="users"
          element={
            <RoleGate id="users">
              <UsersPage />
            </RoleGate>
          }
        />
        <Route
          path="permissions"
          element={
            <RoleGate id="permissions">
              <PermissionsPage />
            </RoleGate>
          }
        />
        <Route
          path="agents"
          element={
            <RoleGate id="agents">
              <AgentsPage />
            </RoleGate>
          }
        />
        <Route
          path="add-agent"
          element={
            <RoleGate id="add-agent">
              <AddAgentPage />
            </RoleGate>
          }
        />
        <Route
          path="audit"
          element={
            <RoleGate id="audit">
              <AuditPage />
            </RoleGate>
          }
        />
        <Route
          path="security"
          element={
            <RoleGate id="security">
              <SecurityPage />
            </RoleGate>
          }
        />
        <Route
          path="plugins"
          element={
            <RoleGate id="plugins">
              <PluginsPage />
            </RoleGate>
          }
        />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
