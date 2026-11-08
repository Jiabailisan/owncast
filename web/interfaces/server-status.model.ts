export interface ServerStatus {
  online: boolean;
  viewerCount: number;
  lastConnectTime?: Date;
  lastDisconnectTime?: Date;
  versionNumber?: string;
  streamTitle?: string;
  // Additive: ingest protocol of the active stream ('' when offline).
  inputProtocol?: 'rtmp' | 'srt' | '';
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
