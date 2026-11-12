import { render, screen } from '@testing-library/react';

import { CurrentVariantsTable } from '../components/admin/CurrentVariantsTable';
import { ServerStatusContext } from '../utils/server-status-context';
import { VideoVariant } from '../types/config-section';

jest.mock('next-export-i18n', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

const baseVariant: VideoVariant = {
  cpuUsageLevel: 3,
  framerate: 24,
  audioPassthrough: true,
  audioBitrate: 0,
  videoPassthrough: false,
  videoBitrate: 1200,
  scaledWidth: 1280,
  scaledHeight: 720,
  name: '720p',
};

// Shape of GET /api/admin/serverconfig after the backend fix: the per-variant
// outputs mapping is carried through the admin payload.
const serverConfigVariants: VideoVariant[] = [
  {
    ...baseVariant,
    name: '720p mapped',
    outputs: {
      hls: { enabled: true },
      webrtc: { enabled: true, mode: 'copy', copyFallback: 'transcode' },
    },
  },
  {
    ...baseVariant,
    name: 'legacy',
    scaledHeight: 360,
    videoBitrate: 600,
  },
];

const renderTable = () => {
  const value = {
    serverConfig: {
      videoSettings: { videoQualityVariants: serverConfigVariants },
    },
    setFieldInConfigState: jest.fn(),
    online: false,
  } as any;
  return render(
    <ServerStatusContext.Provider value={value}>
      <CurrentVariantsTable />
    </ServerStatusContext.Provider>,
  );
};

describe('CurrentVariantsTable WebRTC output column', () => {
  test('shows the mode tag (not Off) for a serverconfig variant with outputs.webrtc.enabled', () => {
    renderTable();
    const modeTag = screen.getByTestId('variant-webrtc-mode');
    expect(modeTag).toHaveTextContent('Copy');
    // A variant without an explicit WebRTC block is delivered over WebRTC anyway,
    // so it must not be labelled Off.
    expect(screen.queryAllByTestId('variant-webrtc-off')).toHaveLength(0);
  });

  test('shows the inherit tag for a legacy variant without outputs', () => {
    renderTable();
    expect(screen.getByText('legacy')).toBeInTheDocument();
    expect(screen.getByTestId('variant-webrtc-inherit')).toHaveTextContent('Follows HLS');
  });
});
