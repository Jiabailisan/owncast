export interface ServerStatus {
  online: boolean;
  viewerCount: number;
  lastConnectTime?: Date;
  lastDisconnectTime?: Date;
  versionNumber?: string;
  streamTitle?: string;
  // Additive: ingest protocol of the active stream ('' when offline).
  inputProtocol?: 'rtmp' | 'srt' | '';
  // Additive stream metering (kbps) and public delivery pipeline label used by
  // the viewer Stats for nerds panel.
  videoBitrate?: number;
  audioBitrate?: number;
  videoPipeline?: string;
  serverTime: Date;
}

export function makeEmptyServerStatus(): ServerStatus {
  return {
    online: false,
    viewerCount: 0,
    inputProtocol: '',
    serverTime: new Date(),
  };
}
