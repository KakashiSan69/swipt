import type { FileHandle } from "@swipt/api/file-system";
import type * as api from "@swipt/api/http";

import { domString, usvString } from "../shared/webidl";
import { invoke } from "./async";
import { eventSourceInit, open, type EventSource } from "./eventsource";
import {
  type Connection,
  Headers,
  headersInit,
  headersOf,
  requestOf,
  sendWith,
  throwIfAborted,
  type Transport,
} from "./fetch";
import { fileLocation } from "./files";
import { URL } from "./url";
import { connect, protocolList, type WebSocket } from "./websocket";

type JarReference = NonNullable<Transport["cookies"]>;

const references = new WeakMap<object, JarReference>();
let jars = 0;

const absoluteURL = (value: unknown, context: string): string => {
  const text = usvString(value, context);
  try {
    return new URL(text).href;
  } catch {
    throw new TypeError(
      `${context} ${JSON.stringify(text)} is not an absolute URL`,
    );
  }
};

export class CookieJar implements api.CookieJar {
  constructor(file?: FileHandle) {
    if (file === undefined) {
      references.set(this, Object.freeze({ id: `jar${++jars}` }));
    } else {
      const location = fileLocation(file);
      if (location === undefined) {
        throw new TypeError("the cookie jar's file is not a FileHandle");
      }
      references.set(
        this,
        Object.freeze({ root: location.root, path: location.path }),
      );
    }
    Object.freeze(this);
  }

  async getAll(url?: string | URL): Promise<api.Cookie[]> {
    return invoke<api.Cookie[]>("cookies.get", [
      references.get(this),
      url === undefined ? "" : absoluteURL(url, "the URL"),
    ]);
  }

  async set(url: string | URL, setCookie: string): Promise<void> {
    await invoke("cookies.set", [
      references.get(this),
      absoluteURL(url, "the URL"),
      domString(setCookie, "the cookie"),
    ]);
  }

  async clear(): Promise<void> {
    await invoke("cookies.clear", [references.get(this)]);
  }
}

const proxySchemes = ["http:", "https:", "socks5:", "socks5h:"];
const pem =
  /-----BEGIN CERTIFICATE-----\s*[A-Za-z0-9+/=\s]+-----END CERTIFICATE-----/;

const transportOf = (options: api.ClientOptions): Transport => {
  const { cookies, proxy, tls } = options;
  const transport: {
    -readonly [Key in keyof Transport]: Transport[Key];
  } = {};
  if (cookies !== undefined) {
    const reference = references.get(cookies as object);
    if (reference === undefined) {
      throw new TypeError("cookies is not a CookieJar");
    }
    transport.cookies = reference;
  }
  if (proxy !== undefined && proxy !== null && proxy !== "") {
    const url = absoluteURL(proxy, "the proxy");
    const scheme = url.slice(0, url.indexOf(":") + 1);
    if (!proxySchemes.includes(scheme)) {
      throw new TypeError(
        `the proxy's scheme is ${scheme}, not one of ${proxySchemes.join(", ")}`,
      );
    }
    transport.proxy = url;
  }
  if (tls !== undefined) {
    if (tls === null || typeof tls !== "object") {
      throw new TypeError("tls is not an object");
    }
    const { ca, rejectUnauthorized } = tls;
    if (ca !== undefined) {
      if (
        typeof ca !== "string" &&
        (ca === null ||
          typeof (ca as Iterable<unknown>)[Symbol.iterator] !== "function")
      ) {
        throw new TypeError("tls.ca is not a string or an array of strings");
      }
      transport.ca = (typeof ca === "string" ? [ca] : [...ca]).map(
        (certificate) => {
          if (typeof certificate !== "string" || !pem.test(certificate)) {
            throw new TypeError(
              "tls.ca has an entry that is not a PEM certificate",
            );
          }
          return certificate;
        },
      );
    }
    if (rejectUnauthorized !== undefined) {
      if (typeof rejectUnauthorized !== "boolean") {
        throw new TypeError("tls.rejectUnauthorized is not a boolean");
      }
      transport.rejectUnauthorized = rejectUnauthorized;
    }
  }
  return Object.freeze(transport);
};

export class Client implements api.Client {
  declare readonly fetch: api.Client["fetch"];
  declare readonly webSocket: api.Client["webSocket"];
  declare readonly eventSource: api.Client["eventSource"];

  constructor(options: api.ClientOptions = {}) {
    if (options === null || typeof options !== "object") {
      throw new TypeError("the client options are not an object");
    }
    const connection: Connection = {
      headers:
        options.headers === undefined
          ? []
          : headersOf(
              new Headers(headersInit(options.headers, "the client's headers")),
            ),
      transport: transportOf(options),
    };
    const fetch = async (
      input: Request | string | URL,
      init?: RequestInit,
    ): Promise<Response> => {
      const request = requestOf(input, init, "Client.fetch");
      throwIfAborted(request);
      return sendWith(request, connection);
    };
    const webSocket = (
      url: string | URL,
      protocols: string | string[] = [],
    ): WebSocket =>
      connect(
        usvString(url, "Client.webSocket argument 1"),
        protocolList(protocols, "Client.webSocket argument 2"),
        connection,
      );
    const eventSource = (
      url: string | URL,
      init?: EventSourceInit,
    ): EventSource =>
      open(
        usvString(url, "Client.eventSource argument 1"),
        eventSourceInit(init, "Client.eventSource argument 2"),
        connection,
      );
    Object.defineProperties(this, {
      fetch: { value: fetch, enumerable: true },
      webSocket: { value: webSocket, enumerable: true },
      eventSource: { value: eventSource, enumerable: true },
    });
    Object.freeze(this);
  }
}
