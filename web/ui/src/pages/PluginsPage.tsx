import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { PluginConfigField, PluginInfo } from "../lib/types";
import { Badge } from "../components/Badge";
import { NotificationPolicyCard } from "../components/NotificationPolicyCard";
import { NotificationTemplatesCard } from "../components/NotificationTemplatesCard";
import { useToast } from "../components/Toast";

function statusOf(p: PluginInfo): { label: string; kind: "ok" | "warn" | "danger" } {
  if (p.last_error) return { label: "error", kind: "danger" };
  if (p.configured) return { label: "configured", kind: "ok" };
  return { label: "not configured", kind: "warn" };
}

// --- nested-object helpers -------------------------------------------------
// Config is a plain JSON object; ConfigField.key is only the leaf name within
// its containing object (nesting comes from `fields` on an "object" field),
// so form state is addressed by a path of keys, e.g. ["rocketchat", "enabled"].

function setPath(obj: Record<string, unknown>, path: string[], value: unknown): Record<string, unknown> {
  if (path.length === 0) return obj;
  const [head, ...rest] = path;
  const existing = obj[head];
  const child = existing && typeof existing === "object" && !Array.isArray(existing) ? (existing as Record<string, unknown>) : {};
  return {
    ...obj,
    [head]: rest.length === 0 ? value : setPath(child, rest, value),
  };
}

/** Build a config object containing only the keys the schema knows about (so stray old keys don't linger), seeded from the current values where present. */
function seedFromSchema(fields: PluginConfigField[], current: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of fields) {
    const v = current[f.key];
    if (f.type === "object") {
      out[f.key] = seedFromSchema(f.fields || [], (v && typeof v === "object" ? (v as Record<string, unknown>) : {}));
    } else if (v !== undefined) {
      out[f.key] = v;
    }
  }
  return out;
}

/** True if every required leaf field (recursively) has a non-empty value. */
function findMissingRequired(fields: PluginConfigField[], values: Record<string, unknown>, path: string[] = []): string[] {
  const missing: string[] = [];
  for (const f of fields) {
    const v = values[f.key];
    const label = [...path, f.label].join(" / ");
    if (f.type === "object") {
      const enabled = (v && typeof v === "object" ? (v as Record<string, unknown>)["enabled"] : undefined) === true;
      if (enabled) {
        missing.push(...findMissingRequired(f.fields || [], (v as Record<string, unknown>) || {}, [...path, f.label]));
      }
      continue;
    }
    if (f.required && (v === undefined || v === null || v === "")) {
      missing.push(label);
    }
  }
  return missing;
}

function SchemaFieldInput({
  field,
  value,
  onChange,
}: {
  field: PluginConfigField;
  value: unknown;
  onChange: (v: unknown) => void;
}) {
  if (field.type === "bool") {
    return (
      <label className="row" style={{ gap: ".5rem", alignItems: "center", fontWeight: 400 }}>
        <input
          type="checkbox"
          checked={value === true}
          onChange={(e) => onChange(e.target.checked)}
          style={{ accentColor: "var(--accent, #f0a742)" }}
        />
        {field.label}
      </label>
    );
  }

  if (field.type === "int") {
    return (
      <input
        type="number"
        value={typeof value === "number" ? value : ""}
        placeholder={field.placeholder}
        onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))}
      />
    );
  }

  // "string" (secret or plain)
  return (
    <input
      type="text"
      className={field.secret ? "mono" : undefined}
      value={typeof value === "string" ? value : ""}
      placeholder={field.secret ? field.placeholder || "leave unchanged to keep the current value" : field.placeholder}
      onChange={(e) => onChange(e.target.value)}
      autoComplete="off"
      spellCheck={false}
    />
  );
}

function SchemaGroup({
  fields,
  values,
  onFieldChange,
  depth = 0,
}: {
  fields: PluginConfigField[];
  values: Record<string, unknown>;
  onFieldChange: (key: string, value: unknown) => void;
  depth?: number;
}) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "0.7rem" }}>
      {fields.map((f) => {
        const v = values[f.key];

        if (f.type === "object") {
          const sub = (v && typeof v === "object" ? (v as Record<string, unknown>) : {}) as Record<string, unknown>;
          return (
            <div
              key={f.key}
              style={{
                border: "1px solid var(--border, #333)",
                borderRadius: "8px",
                padding: "0.65rem 0.8rem",
                marginLeft: depth ? "0.5rem" : 0,
              }}
            >
              <div className="row" style={{ justifyContent: "space-between" }}>
                <strong style={{ fontSize: ".9rem" }}>{f.label}</strong>
              </div>
              {f.help ? (
                <p className="muted" style={{ fontSize: ".78rem", margin: ".2rem 0 .5rem" }}>
                  {f.help}
                </p>
              ) : null}
              <SchemaGroup
                fields={f.fields || []}
                values={sub}
                onFieldChange={(childKey, childValue) => {
                  onFieldChange(f.key, { ...sub, [childKey]: childValue });
                }}
                depth={depth + 1}
              />
            </div>
          );
        }

        return (
          <div key={f.key}>
            {f.type !== "bool" ? (
              <label className="field">
                {f.label}
                {f.required ? <span style={{ color: "var(--danger, #e5484d)" }}> *</span> : null}
                {f.secret ? (
                  <span className="muted" style={{ fontWeight: 400, fontSize: ".78rem" }}>
                    {" "}
                    (secret{typeof v === "string" && v ? ` — currently ${v}` : ""})
                  </span>
                ) : null}
              </label>
            ) : null}
            <SchemaFieldInput field={f} value={v} onChange={(nv) => onFieldChange(f.key, nv)} />
            {f.help ? (
              <p className="muted" style={{ fontSize: ".78rem", margin: ".25rem 0 0" }}>
                {f.help}
              </p>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}

function PluginCard({ plugin }: { plugin: PluginInfo }) {
  const { toast } = useToast();
  const qc = useQueryClient();
  const [expanded, setExpanded] = useState(false);
  const [formValues, setFormValues] = useState<Record<string, unknown>>({});
  const [rawText, setRawText] = useState(""); // fallback path only, for schema-less plugins
  const [initializedFor, setInitializedFor] = useState<string | null>(null);
  const [validationError, setValidationError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [saving, setSaving] = useState(false);
  const [toggling, setToggling] = useState(false);

  const hasSchema = Boolean(plugin.schema && plugin.schema.length > 0);

  // Seed the form from the plugin's current config the first time it's opened; never clobber
  // in-progress edits on background refetches.
  useEffect(() => {
    if (expanded && initializedFor !== plugin.name) {
      if (hasSchema) {
        setFormValues(seedFromSchema(plugin.schema!, plugin.config || {}));
      } else {
        setRawText(JSON.stringify(plugin.config || {}, null, 2));
      }
      setInitializedFor(plugin.name);
      setValidationError("");
      setSaveError("");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expanded, plugin.name, plugin.config, initializedFor, hasSchema]);

  async function toggle() {
    setToggling(true);
    try {
      await api<{ plugin: PluginInfo }>(
        `/admin/plugins/${encodeURIComponent(plugin.name)}/${plugin.enabled ? "disable" : "enable"}`,
        { method: "POST", body: "{}" },
      );
      toast(`${plugin.name} ${plugin.enabled ? "disabled" : "enabled"}`);
      void qc.invalidateQueries({ queryKey: ["plugins"] });
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    } finally {
      setToggling(false);
    }
  }

  async function save() {
    setValidationError("");
    setSaveError("");

    let config: unknown;
    if (hasSchema) {
      const missing = findMissingRequired(plugin.schema!, formValues);
      if (missing.length) {
        setValidationError(`Required: ${missing.join(", ")}`);
        return;
      }
      config = formValues;
    } else {
      try {
        config = rawText.trim() ? JSON.parse(rawText) : {};
      } catch (e) {
        setValidationError(e instanceof Error ? e.message : "Invalid JSON");
        return;
      }
      if (!config || typeof config !== "object" || Array.isArray(config)) {
        setValidationError("Config must be a JSON object");
        return;
      }
    }

    setSaving(true);
    try {
      const res = await api<{ plugin: PluginInfo; configure_error?: string }>(
        `/admin/plugins/${encodeURIComponent(plugin.name)}/config`,
        { method: "PUT", body: JSON.stringify({ enabled: plugin.enabled, config }) },
      );
      void qc.invalidateQueries({ queryKey: ["plugins"] });
      if (res.configure_error) {
        setSaveError(res.configure_error);
        toast(`Saved, but ${plugin.name} failed to initialize`, "err");
      } else {
        toast(`${plugin.name} config saved`);
      }
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  const status = statusOf(plugin);

  return (
    <div className="card" style={{ marginBottom: "0.85rem" }}>
      <div className="row" style={{ justifyContent: "space-between", alignItems: "flex-start" }}>
        <div>
          <div className="row">
            <strong>{plugin.name}</strong>
            <span className="muted mono" style={{ fontSize: ".78rem" }}>
              v{plugin.version || "0"}
            </span>
            {plugin.has_webhook ? <Badge status="neutral">webhook</Badge> : null}
            <Badge status={plugin.enabled ? "ok" : "neutral"}>{plugin.enabled ? "enabled" : "disabled"}</Badge>
            <Badge status={status.kind}>{status.label}</Badge>
          </div>
          {plugin.last_error ? (
            <div className="err" style={{ marginTop: ".35rem", fontSize: ".85rem" }}>
              {plugin.last_error}
            </div>
          ) : null}
        </div>
        <div className="row">
          <button type="button" className="btn secondary sm" disabled={toggling} onClick={() => void toggle()}>
            {toggling ? "…" : plugin.enabled ? "Disable" : "Enable"}
          </button>
          <button type="button" className="btn secondary sm" onClick={() => setExpanded((v) => !v)}>
            {expanded ? "Close" : "Edit config"}
          </button>
        </div>
      </div>

      {expanded ? (
        <div style={{ marginTop: "0.9rem" }}>
          <p className="muted" style={{ fontSize: ".82rem" }}>
            Secret fields show the first/last few characters of the stored value (e.g.{" "}
            <span className="mono">xoxb****9f2c</span>) so you can tell which credential is set without exposing it.
            Leave a secret field showing that pattern untouched to keep it as-is — only type into it to replace the
            value.
          </p>

          {hasSchema ? (
            <SchemaGroup fields={plugin.schema!} values={formValues} onFieldChange={(k, v) => setFormValues((prev) => setPath(prev, [k], v))} />
          ) : (
            <>
              <label className="field">Config (JSON)</label>
              <textarea className="mono" rows={14} value={rawText} onChange={(e) => setRawText(e.target.value)} spellCheck={false} />
            </>
          )}

          {validationError ? <div className="err">{validationError}</div> : null}
          {saveError ? <div className="err">{saveError}</div> : null}
          <div className="row" style={{ marginTop: "0.65rem" }}>
            <button type="button" className="btn sm" disabled={saving} onClick={() => void save()}>
              {saving ? "Saving…" : "Save config"}
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}

export function PluginsPage() {
  const q = useQuery({
    queryKey: ["plugins"],
    queryFn: () => api<{ plugins: PluginInfo[] }>("/admin/plugins"),
  });
  const plugins = [...(q.data?.plugins || [])].sort((a, b) => a.name.localeCompare(b.name));

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Plugins</h1>
          <p>Turn integrations on or off. Changes apply without a restart.</p>
        </div>
      </div>

      {q.isLoading ? (
        <div className="card">
          <p className="muted">Loading plugins…</p>
        </div>
      ) : null}

      {q.isError ? (
        <div className="card">
          <div className="err">{q.error instanceof Error ? q.error.message : "Failed to load plugins"}</div>
        </div>
      ) : null}

      {!q.isLoading && !q.isError && plugins.length === 0 ? (
        <div className="card">
          <div className="empty">No plugins registered on this server.</div>
        </div>
      ) : null}

      <NotificationPolicyCard />

      <NotificationTemplatesCard />

      {plugins.map((p) => (
        <PluginCard key={p.name} plugin={p} />
      ))}
    </>
  );
}
