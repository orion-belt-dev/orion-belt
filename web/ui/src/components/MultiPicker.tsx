import { useId, useMemo, useState } from "react";

// A filterable checkbox list for choosing several users or machines at once.
export function MultiPicker<T>({
  legend,
  items,
  getId,
  getLabel,
  selected,
  onChange,
}: {
  legend: string;
  items: T[];
  getId: (item: T) => string;
  getLabel: (item: T) => string;
  selected: string[];
  onChange: (ids: string[]) => void;
}) {
  const [filter, setFilter] = useState("");
  const id = useId();
  const shown = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    return needle ? items.filter((i) => getLabel(i).toLowerCase().includes(needle)) : items;
  }, [items, filter, getLabel]);
  const chosen = new Set(selected);

  function toggle(itemId: string) {
    const next = new Set(chosen);
    if (next.has(itemId)) next.delete(itemId);
    else next.add(itemId);
    onChange([...next]);
  }

  return (
    <fieldset className="picker">
      <legend className="field">
        {legend} <span className="muted">({selected.length} selected)</span>
      </legend>
      <input
        type="search"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder={`Filter ${legend.toLowerCase()}…`}
        aria-label={`Filter ${legend.toLowerCase()}`}
        aria-controls={`${id}-list`}
      />
      <div className="row picker-actions">
        <button
          type="button"
          className="btn secondary sm"
          onClick={() => onChange([...new Set([...selected, ...shown.map(getId)])])}
          disabled={shown.length === 0}
        >
          Select {filter ? "shown" : "all"}
        </button>
        <button type="button" className="btn secondary sm" onClick={() => onChange([])} disabled={selected.length === 0}>
          Clear
        </button>
      </div>
      <ul className="picker-list" id={`${id}-list`}>
        {shown.map((item) => {
          const itemId = getId(item);
          return (
            <li key={itemId}>
              <label>
                <input type="checkbox" checked={chosen.has(itemId)} onChange={() => toggle(itemId)} />
                {getLabel(item)}
              </label>
            </li>
          );
        })}
        {shown.length === 0 ? <li className="muted">No match.</li> : null}
      </ul>
    </fieldset>
  );
}
