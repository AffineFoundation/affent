import { readEventStream, type StreamEventsOptions } from "./stream";

export interface ApiClientOptions {
  basePath?: string;
  authToken?: string;
  fetchImpl?: typeof fetch;
}

export interface JsonRequestOptions {
  method?: "GET" | "POST" | "PATCH" | "DELETE";
  body?: unknown;
  signal?: AbortSignal;
}

export interface RawRequestOptions {
  method?: "GET" | "POST" | "PATCH" | "DELETE";
  signal?: AbortSignal;
  accept?: string;
}

export class ApiError extends Error {
  readonly status: number;
  readonly type?: string;

  constructor(status: number, message: string, type?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.type = type;
  }
}

interface ServerErrorBody {
  error?: {
    message?: unknown;
    type?: unknown;
  };
}

export class ApiClient {
  private readonly basePath: string;
  private readonly authToken?: string;
  private readonly fetchImpl: typeof fetch;

  constructor(options: ApiClientOptions = {}) {
    this.basePath = (options.basePath ?? "").replace(/\/$/, "");
    this.authToken = options.authToken;
    this.fetchImpl = options.fetchImpl ?? globalThis.fetch.bind(globalThis);
  }

  async json<T>(path: string, options: JsonRequestOptions = {}): Promise<T> {
    const headers = new Headers();
    headers.set("Accept", "application/json");
    if (options.body !== undefined) headers.set("Content-Type", "application/json");
    if (this.authToken) headers.set("Authorization", `Bearer ${this.authToken}`);

    const resp = await this.fetchImpl(this.url(path), {
      method: options.method ?? (options.body === undefined ? "GET" : "POST"),
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal,
    });

    if (!resp.ok) {
      throw await toApiError(resp);
    }
    if (resp.status === 204) {
      return undefined as T;
    }
    try {
      return (await resp.json()) as T;
    } catch {
      throw invalidJsonResponse(path, resp);
    }
  }

  async streamEvents(path: string, options: StreamEventsOptions): Promise<void> {
    const headers = new Headers();
    headers.set("Accept", "text/event-stream");
    if (options.lastEventId != null) headers.set("Last-Event-ID", String(options.lastEventId));
    if (this.authToken) headers.set("Authorization", `Bearer ${this.authToken}`);

    const resp = await this.fetchImpl(this.url(path), {
      method: "GET",
      headers,
      signal: options.signal,
    });
    if (!resp.ok) {
      throw await toApiError(resp);
    }
    await readEventStream(resp, options);
  }

  async raw(path: string, options: RawRequestOptions = {}): Promise<Response> {
    const headers = new Headers();
    if (options.accept) headers.set("Accept", options.accept);
    if (this.authToken) headers.set("Authorization", `Bearer ${this.authToken}`);

    const resp = await this.fetchImpl(this.url(path), {
      method: options.method ?? "GET",
      headers,
      signal: options.signal,
    });
    if (!resp.ok) {
      throw await toApiError(resp);
    }
    return resp;
  }

  private url(path: string): string {
    return `${this.basePath}${path.startsWith("/") ? path : `/${path}`}`;
  }
}

function invalidJsonResponse(path: string, resp: Response): ApiError {
  const contentType = resp.headers.get("Content-Type") ?? "unknown content type";
  const fromHtmlFallback = contentType.toLowerCase().includes("text/html");
  const message = fromHtmlFallback
    ? `API route ${path} returned the WebUI app shell. The affentserve build may not expose this route, or the WebUI API proxy may point at the frontend instead of the API.`
    : `API returned invalid JSON for ${path} (${contentType}).`;
  return new ApiError(resp.status, message, "invalid_api_response");
}

async function toApiError(resp: Response): Promise<ApiError> {
  let message = `${resp.status} ${resp.statusText}`.trim();
  let type: string | undefined;

  try {
    const body = (await resp.json()) as ServerErrorBody;
    const serverMessage = body.error?.message;
    const serverType = body.error?.type;
    if (typeof serverMessage === "string" && serverMessage.trim() !== "") {
      message = serverMessage;
    }
    if (typeof serverType === "string" && serverType.trim() !== "") {
      type = serverType;
    }
  } catch {
    // Some proxies return HTML or empty bodies. Keep the HTTP status as
    // the actionable fallback instead of masking it with a JSON parse error.
  }

  return new ApiError(resp.status, message, type);
}
