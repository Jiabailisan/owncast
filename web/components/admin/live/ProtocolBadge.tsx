import { FC } from 'react';
import { Tag } from 'antd';

import { IngestProtocol } from '../../../interfaces/live-status.model';

export type ProtocolBadgeProps = {
  protocol: IngestProtocol;
};

// Ingest protocol badge. This is the only protocol-specific branch allowed in
// the live status UI (spec 6.1); all fields render identically for RTMP/SRT.
export const ProtocolBadge: FC<ProtocolBadgeProps> = ({ protocol }) => {
  if (protocol === 'rtmp') return <Tag color="purple">RTMP</Tag>;
  if (protocol === 'srt') return <Tag color="geekblue">SRT</Tag>;
  return <Tag color="default">-</Tag>;
};

export default ProtocolBadge;
