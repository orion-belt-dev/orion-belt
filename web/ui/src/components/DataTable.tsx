import { useCallback, useState } from "react";
import type { ReactNode } from "react";

export type SortDir = "asc" | "desc";

export function useTableState<T>(opts?: { pageSize?: number }) {
  const [query, setQuery] = useState("");
  const [sortKey, setSortKey] = useState<string>("");
  const [sortDir, setSortDir] = useState<SortDir>("asc");
  const [page, setPage] = useState(0);
  const pageSize = opts?.pageSize ?? 25;

  function toggleSort(key: string) {
    if (sortKey === key) setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    else {
      setSortKey(key);
      setSortDir("asc");
    }
    setPage(0);
  }

  // Stable while the table state is unchanged, so callers can memoise on it.
  const process = useCallback(
    (
      rows: T[],
      getSortValue: (row: T, key: string) => string | number | null | undefined,
      searchText: (row: T) => string,
    ) => {
    const needle = query.trim().toLowerCase();
    let filtered = rows;
    if (needle) {
      filtered = rows.filter((r) => searchText(r).toLowerCase().includes(needle));
    }
    if (sortKey) {
      filtered = filtered.toSorted((a, b) => {
        const av = getSortValue(a, sortKey);
        const bv = getSortValue(b, sortKey);
        const aEmpty = av == null || av === "";
        const bEmpty = bv == null || bv === "";
        if (aEmpty && bEmpty) return 0;
        if (aEmpty) return 1;
        if (bEmpty) return -1;
        let cmp = 0;
        if (typeof av === "number" && typeof bv === "number") cmp = av - bv;
        else cmp = String(av).localeCompare(String(bv), undefined, { numeric: true, sensitivity: "base" });
        return sortDir === "asc" ? cmp : -cmp;
      });
    }
    const total = filtered.length;
    const pageCount = Math.max(1, Math.ceil(total / pageSize) || 1);
    const safePage = Math.min(page, Math.max(0, pageCount - 1));
    const start = safePage * pageSize;
    return { rows: filtered.slice(start, start + pageSize), total, pageCount, page: safePage, start };
    },
    [query, sortKey, sortDir, page, pageSize],
  );

  return {
    query,
    setQuery: (q: string) => {
      setQuery(q);
      setPage(0);
    },
    sortKey,
    sortDir,
    toggleSort,
    page,
    setPage,
    pageSize,
    process,
  };
}

export function SortTh({
  label,
  col,
  sortKey,
  sortDir,
  onSort,
}: {
  label: string;
  col: string;
  sortKey: string;
  sortDir: SortDir;
  onSort: (col: string) => void;
}) {
  const active = sortKey === col;
  return (
    <th>
      <button type="button" className={`th-sort${active ? " active" : ""}`} onClick={() => onSort(col)}>
        {label}
        <span className="th-ind">{active ? (sortDir === "asc" ? "↑" : "↓") : "↕"}</span>
      </button>
    </th>
  );
}

export function TableToolbar({
  query,
  onQuery,
  placeholder,
  children,
}: {
  query: string;
  onQuery: (q: string) => void;
  placeholder?: string;
  children?: ReactNode;
}) {
  return (
    <div className="table-toolbar">
      <input
        className="table-search"
        value={query}
        onChange={(e) => onQuery(e.target.value)}
        placeholder={placeholder || "Filter…"}
        aria-label="Filter table"
      />
      {children}
    </div>
  );
}

export function Pagination({
  page,
  pageCount,
  total,
  pageSize,
  onPage,
}: {
  page: number;
  pageCount: number;
  total: number;
  pageSize: number;
  onPage: (p: number) => void;
}) {
  if (total <= pageSize) {
    return total ? (
      <div className="pager muted">
        {total} item{total === 1 ? "" : "s"}
      </div>
    ) : null;
  }
  const from = page * pageSize + 1;
  const to = Math.min(total, (page + 1) * pageSize);
  return (
    <div className="pager">
      <span className="muted">
        {from}–{to} of {total}
      </span>
      <div className="row">
        <button type="button" className="btn secondary sm" disabled={page <= 0} onClick={() => onPage(page - 1)}>
          Prev
        </button>
        <span className="muted mono">
          {page + 1}/{pageCount}
        </span>
        <button type="button" className="btn secondary sm" disabled={page >= pageCount - 1} onClick={() => onPage(page + 1)}>
          Next
        </button>
      </div>
    </div>
  );
}
