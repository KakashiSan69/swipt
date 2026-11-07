import type { CallOptions, ObservableState } from "./index";
import type { DirectoryHandle, FileHandle } from "./file-system";
import type { RasterImage } from "./image";

export type ScreenStreamOptions = CallOptions &
  (
    | { readonly mode?: "remoteDesktop"; readonly maximumBitrate?: number }
    | { readonly mode: "coreDevice" }
  );

export interface RecordingSession {
  stop(options?: CallOptions): Promise<void>;
}

export interface ScreenStream {
  readonly frames: ObservableState<RasterImage>;
  record(
    directory: DirectoryHandle,
    options?: CallOptions,
  ): Promise<RecordingSession>;
  close(options?: CallOptions): Promise<void>;
}

export interface ScreenRecording {
  readonly start: number;
  readonly duration: number;
  frame(time: number): RasterImage;
  export(file: FileHandle, options?: CallOptions): Promise<void>;
}

export declare function open(
  options?: ScreenStreamOptions,
): Promise<ScreenStream>;
export declare function openRecording(
  directory: DirectoryHandle,
  options?: CallOptions,
): Promise<ScreenRecording>;
