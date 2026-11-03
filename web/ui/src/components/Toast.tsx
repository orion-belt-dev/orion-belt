import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";

type ToastState = { message: string; kind?: string } | null;

const ToastContext = createContext<{
  toast: (message: string, kind?: string) => void;
} | null>(null);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [current, setCurrent] = useState<ToastState>(null);

  const toast = useCallback((message: string, kind?: string) => {
    setCurrent({ message, kind });
    window.setTimeout(() => setCurrent(null), 3200);
  }, []);

  const value = useMemo(() => ({ toast }), [toast]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <output className={`toast${current ? " show" : ""}${current?.kind === "err" ? " err" : ""}`}>{current?.message}</output>
    </ToastContext.Provider>
  );
}

export function useToast() {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast outside provider");
  return ctx;
}
