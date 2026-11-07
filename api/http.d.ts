import type { FileHandle } from "./file-system";

export interface Cookie {
  readonly name: string;
  readonly value: string;
  readonly domain: string;
  readonly hostOnly: boolean;
  readonly path: string;
  readonly expires: number | null;
  readonly secure: boolean;
  readonly httpOnly: boolean;
  readonly sameSite: "strict" | "lax" | "none" | null;
}

export declare class CookieJar {
  constructor(file?: FileHandle);
  getAll(url?: string | URL): Promise<Cookie[]>;
  set(url: string | URL, setCookie: string): Promise<void>;
  clear(): Promise<void>;
}

export interface ClientOptions {
  readonly cookies?: CookieJar;
  readonly headers?: Headers | Record<string, string> | [string, string][];
  readonly proxy?: string;
  readonly tls?: {
    readonly ca?: string | readonly string[];
    readonly rejectUnauthorized?: boolean;
  };
}

export declare class Client {
  constructor(options?: ClientOptions);
  readonly fetch: (
    input: string | URL | Request,
    init?: RequestInit,
  ) => Promise<Response>;
  readonly webSocket: (
    url: string | URL,
    protocols?: string | string[],
  ) => WebSocket;
  readonly eventSource: (
    url: string | URL,
    init?: EventSourceInit,
  ) => EventSource;
}
