import { useState, type ReactNode } from "react";
import {
  ArrowRight,
  BookOpen,
  Check,
  Copy,
  FileQuestion,
  LoaderCircle,
  RefreshCw,
  SearchX,
  TriangleAlert,
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Query } from "./api";
import { useT } from "./i18n";
import type { Insight } from "./types";
import { Link, routeURL, useRouter } from "./router";

const palette: Record<string, string> = {
  preference: "#8b6bb4",
  decision: "#cc9954",
  insight: "#579581",
  fact: "#668bb4",
  context: "#bc7e8f",
  general: "#93918c",
};
export function categoryColor(category: string) {
  return palette[category] || "#93918c";
}
export function humanize(value: string) {
  return value
    .replaceAll("_", " ")
    .replace(/^./, (letter) => letter.toUpperCase());
}
export function title(content: string) {
  return (
    content
      .replace(/^\s*#+\s*/gm, "")
      .replace(/[*_`>\[\]]/g, "")
      .split("\n")
      .find((line) => line.trim())
      ?.slice(0, 110) || ""
  );
}
export function excerpt(content: string) {
  return content
    .replace(/[#*_`>\[\]]/g, "")
    .replace(/\s+/g, " ")
    .slice(0, 230);
}
export function date(value: string, full = false) {
  if (!value) return "";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return new Intl.DateTimeFormat(
    undefined,
    full
      ? { dateStyle: "medium", timeStyle: "short" }
      : { month: "short", day: "numeric", year: "numeric" },
  ).format(parsed);
}
export function Category({ value }: { value: string }) {
  return (
    <span className="category">
      <span
        className="category-dot"
        style={{ background: categoryColor(value) }}
      />
      {humanize(value)}
    </span>
  );
}
export function Importance({ value }: { value: number }) {
  const t = useT();
  const label = t("component.importanceLabel", { value });
  return (
    <span className="importance" aria-label={label} title={label}>
      {[1, 2, 3, 4, 5].map((level) => (
        <i key={level} className={level <= value ? "filled" : ""} />
      ))}
    </span>
  );
}
export function Empty({
  title: heading,
  children,
  icon = "empty",
}: {
  title: string;
  children: ReactNode;
  icon?: "empty" | "search" | "error";
}) {
  const Icon =
    icon === "search"
      ? SearchX
      : icon === "error"
        ? TriangleAlert
        : FileQuestion;
  return (
    <div className="empty-state">
      <div className="empty-icon">
        <Icon size={25} />
      </div>
      <h2>{heading}</h2>
      <div>{children}</div>
    </div>
  );
}
export function QueryView<T>({
  query,
  children,
  unavailable = false,
}: {
  query: Query<T>;
  children: (data: T) => ReactNode;
  unavailable?: boolean;
}) {
  const t = useT();
  if (!query.data && query.loading)
    return (
      <div className="loading-state" role="status">
        <LoaderCircle className="spin" size={22} />
        <span>{t("state.loadingKnowledge")}</span>
        <div className="skeleton" />
        <div className="skeleton short" />
        <div className="skeleton" />
      </div>
    );
  if (!query.data && query.error)
    return (
      <Empty
        title={t(
          unavailable ? "state.memoryUnavailable" : "state.unableToOpen",
        )}
        icon="error"
      >
        <p>{query.error.message}</p>
        <button className="button" onClick={query.refresh}>
          <RefreshCw size={15} />
          {t("state.tryAgain")}
        </button>
      </Empty>
    );
  if (!query.data) return null;
  return (
    <>
      {query.error && (
        <div className="notice warning" role="alert">
          <TriangleAlert size={17} />
          <div>
            <strong>{t("state.staleData")}</strong>{" "}
            {t("state.refreshFailed", { message: query.error.message })}
          </div>
          <button onClick={query.refresh}>{t("state.retry")}</button>
        </div>
      )}
      {children(query.data)}
    </>
  );
}
export function PageHeading({
  eyebrow,
  title: heading,
  description,
  action,
}: {
  eyebrow: string;
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <header className="page-heading">
      <div>
        <div className="eyebrow">{eyebrow}</div>
        <h1>{heading}</h1>
        <p>{description}</p>
      </div>
      {action}
    </header>
  );
}
export function LastUpdated({
  value,
  loading,
}: {
  value: string;
  loading?: boolean;
}) {
  const t = useT();
  return (
    <span className="last-updated">
      <span className={loading ? "live-dot busy" : "live-dot"} />
      {loading
        ? t("state.refreshing")
        : t("state.updated", { date: date(value, true) })}
    </span>
  );
}
export function MemoryCard({
  memory,
  compact = false,
}: {
  memory: Insight;
  compact?: boolean;
}) {
  const { route } = useRouter();
  const t = useT();
  return (
    <Link
      href={routeURL(route.store, "memories", route.search, memory.id)}
      className={`memory-card ${compact ? "compact" : ""}`}
    >
      <div className="memory-card-top">
        <Category value={memory.category} />
        <Importance value={memory.importance} />
      </div>
      <h3>{title(memory.content) || t("state.untitled")}</h3>
      {!compact && <p>{excerpt(memory.content)}</p>}
      <div className="memory-card-bottom">
        <span className="source" title={memory.source}>
          {memory.source || t("state.noSource")}
        </span>
        <time dateTime={memory.created_at}>
          {date(memory.created_at) || t("state.unknownDate")}
        </time>
      </div>
      {!compact && !!memory.entities?.length && (
        <div className="card-entities">
          {memory.entities.slice(0, 3).map((entity) => (
            <span key={entity}>↗ {entity}</span>
          ))}
          {memory.entities.length > 3 && (
            <span>+{memory.entities.length - 3}</span>
          )}
        </div>
      )}
    </Link>
  );
}
export function SectionTitle({
  children,
  href,
  label,
}: {
  children: ReactNode;
  href?: string;
  label?: string;
}) {
  const t = useT();
  return (
    <div className="section-heading">
      <h2>{children}</h2>
      {href && (
        <Link href={href}>
          {label ?? t("component.viewAll")}
          <ArrowRight size={14} />
        </Link>
      )}
    </div>
  );
}
export function CopyButton({ value, label }: { value: string; label: string }) {
  const t = useT();
  const [state, setState] = useState<"idle" | "done" | "error">("idle");
  return (
    <button
      className="button subtle"
      aria-label={label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setState("done");
        } catch {
          setState("error");
        }
      }}
    >
      {state === "done" ? <Check size={15} /> : <Copy size={15} />}
      {state === "done"
        ? t("component.copied")
        : state === "error"
          ? t("component.copyUnavailable")
          : label}
    </button>
  );
}
export function Markdown({ content }: { content: string }) {
  const t = useT();
  return (
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          h1: ({ children }) => <h2>{children}</h2>,
          img: ({ alt }) => (
            <span className="image-blocked">
              {t("component.imageHidden", { alt: alt ? `: ${alt}` : "" })}
            </span>
          ),
          a: ({ href, children }) => (
            <a href={href} target="_blank" rel="noopener noreferrer">
              {children}
            </a>
          ),
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
export function ReaderLink({
  id,
  children,
}: {
  id: string;
  children?: ReactNode;
}) {
  const { route } = useRouter();
  return (
    <Link
      href={routeURL(route.store, "memories", route.search, id)}
      className="inline-link"
    >
      <BookOpen size={14} />
      {children || id}
    </Link>
  );
}
