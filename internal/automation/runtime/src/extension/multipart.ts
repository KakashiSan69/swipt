import { blobBytes, File } from "./blob";
import { crypto } from "./crypto";
import { TextDecoder, TextEncoder } from "./encoding";
import { FormData } from "./form";
import { URLSearchParams } from "./url";

const lines = (text: string) => text.replace(/\r\n|\r|\n/g, "\r\n");
const quoted = (text: string) =>
  text.replace(/\n/g, "%0A").replace(/\r/g, "%0D").replace(/"/g, "%22");

export function encode(entries: readonly [string, string | File][]): {
  bytes: Uint8Array;
  type: string;
} {
  const boundary = `----SwiptFormBoundary${crypto.randomUUID().replace(/-/g, "")}`;
  const type = `multipart/form-data; boundary=${boundary}`;
  const encoder = new TextEncoder();
  const parts: Uint8Array[] = [];
  for (const [name, value] of entries) {
    const head = `--${boundary}\r\nContent-Disposition: form-data; name="${quoted(lines(name))}"`;
    if (typeof value === "string") {
      parts.push(encoder.encode(`${head}\r\n\r\n${lines(value)}\r\n`));
    } else {
      parts.push(
        encoder.encode(
          `${head}; filename="${quoted(value.name)}"\r\nContent-Type: ${value.type || "application/octet-stream"}\r\n\r\n`,
        ),
        blobBytes(value),
        encoder.encode("\r\n"),
      );
    }
  }
  parts.push(encoder.encode(`--${boundary}--\r\n`));
  const bytes = new Uint8Array(
    parts.reduce((sum, part) => sum + part.length, 0),
  );
  let offset = 0;
  for (const part of parts) {
    bytes.set(part, offset);
    offset += part.length;
  }
  return { bytes, type };
}

const latin1 = (bytes: Uint8Array): string => {
  let text = "";
  for (let index = 0; index < bytes.length; index += 0x8000) {
    text += String.fromCharCode(...bytes.subarray(index, index + 0x8000));
  }
  return text;
};

const parameter = (header: string, name: string): string | undefined => {
  const match = new RegExp(
    `;\\s*${name}\\s*=\\s*(?:"((?:[^"\\\\]|\\\\.)*)"|([^;\\s]*))`,
    "i",
  ).exec(header);
  if (match === null) {
    return undefined;
  }
  return match[1] !== undefined ? match[1].replace(/\\(.)/g, "$1") : match[2];
};

const malformed = () =>
  new TypeError("the body is not valid multipart/form-data");

function parseMultipart(bytes: Uint8Array, contentType: string): FormData {
  const boundary = parameter(contentType, "boundary");
  if (!boundary) {
    throw new TypeError("the multipart/form-data content type has no boundary");
  }
  const text = latin1(bytes);
  const delimiter = `--${boundary}`;
  const form = new FormData();
  const decoder = new TextDecoder();
  let position = text.indexOf(delimiter);
  if (position < 0) {
    throw malformed();
  }
  for (;;) {
    position += delimiter.length;
    if (text.startsWith("--", position)) {
      return form;
    }
    if (!text.startsWith("\r\n", position)) {
      throw malformed();
    }
    position += 2;
    const headerEnd = text.indexOf("\r\n\r\n", position);
    if (headerEnd < 0) {
      throw malformed();
    }
    const headers = new Map<string, string>();
    for (let start = position; start < headerEnd;) {
      const found = text.indexOf("\r\n", start);
      const stop = found < 0 || found > headerEnd ? headerEnd : found;
      const colon = text.indexOf(":", start);
      if (colon > start && colon < stop) {
        headers.set(
          text.slice(start, colon).trim().toLowerCase(),
          decoder.decode(bytes.subarray(colon + 1, stop)).trim(),
        );
      }
      start = stop + 2;
    }
    const end = text.indexOf(`\r\n${delimiter}`, headerEnd + 4);
    if (end < 0) {
      throw malformed();
    }
    const disposition = headers.get("content-disposition") ?? "";
    const name = parameter(disposition, "name");
    if (!/^form-data\b/i.test(disposition) || name === undefined) {
      throw malformed();
    }
    const body = bytes.subarray(headerEnd + 4, end);
    const filename = parameter(disposition, "filename");
    if (filename === undefined) {
      form.append(name, decoder.decode(body));
    } else {
      form.append(
        name,
        new File([body], filename, {
          type: headers.get("content-type") ?? "text/plain",
        }),
      );
    }
    position = end + 2;
  }
}

export function parse(bytes: Uint8Array, contentType: string | null): FormData {
  const essence = (contentType ?? "").split(";")[0].trim().toLowerCase();
  if (essence === "multipart/form-data") {
    return parseMultipart(bytes, contentType!);
  }
  if (essence === "application/x-www-form-urlencoded") {
    const form = new FormData();
    for (const [name, value] of new URLSearchParams(
      new TextDecoder().decode(bytes),
    )) {
      form.append(name, value);
    }
    return form;
  }
  throw new TypeError(
    `a body of ${JSON.stringify(contentType ?? "")} can't be read as FormData`,
  );
}
