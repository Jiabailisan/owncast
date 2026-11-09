import { FC, ReactNode } from 'react';
import { Alert } from 'antd';

import styles from './video-hint-alert.module.scss';

export type VideoHintAlertProps = {
  message: ReactNode;
  description?: ReactNode;
  className?: string;
  testId?: string;
};

// Shared info hint above the Video page variants table. Ant Design renders the
// description of a message+description alert smaller than the message, which
// made the two hints historically inconsistent; one component keeps message and
// description at the same 14px/22px typography, color and spacing, with the
// same info icon and closable behavior.
export const VideoHintAlert: FC<VideoHintAlertProps> = ({
  message,
  description,
  className,
  testId,
}) => (
  <Alert
    type="info"
    showIcon
    closable
    data-testid={testId}
    className={className ? styles.hint + ' ' + className : styles.hint}
    message={message}
    {...(description ? { description } : {})}
  />
);

export default VideoHintAlert;
