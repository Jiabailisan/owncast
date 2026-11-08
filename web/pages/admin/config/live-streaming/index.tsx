import { ReactElement, useCallback, useEffect, useState } from 'react';
import { Alert, Button, Tabs, Typography } from 'antd';

import AdminSettingsService from '../../../../services/admin-settings-service';
import { SettingsMetadata } from '../../../../interfaces/settings-metadata.model';
import { Localization } from '../../../../types/localization';
import { Translation } from '../../../../components/ui/Translation/Translation';
import { AdminLayout } from '../../../../components/layouts/AdminLayout';
import { SrtConfigSection } from '../../../../components/admin/config/live-streaming/SrtConfigSection';
import { WebRtcConfigSection } from '../../../../components/admin/config/live-streaming/WebRtcConfigSection';
import { LiveStreamStatusSection } from '../../../../components/admin/config/live-streaming/LiveStreamStatusSection';
import styles from '../../../../components/admin/config/live-streaming/live-streaming-sections.module.scss';

const { Title, Paragraph } = Typography;

// Configuration -> Live Streaming: SRT ingest, WebRTC delivery and the shared
// live stream status surface. The settings metadata (single source of truth for
// defaults/ranges/apply modes) is fetched once here and passed to both forms.
export default function ConfigLiveStreaming() {
  const [metadata, setMetadata] = useState<SettingsMetadata | null>(null);
  const [metadataError, setMetadataError] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState('srt');

  const loadMetadata = useCallback(async () => {
    setMetadataError(null);
    try {
      setMetadata(await AdminSettingsService.getSettingsMetadata());
    } catch (error) {
      // Expected when the server predates these endpoints: the settings forms
      // show a retry state instead of crashing the page.
      console.warn('Unable to load settings metadata:', (error as Error)?.message);
      setMetadataError((error as Error)?.message || 'Unable to load settings metadata');
    }
  }, []);

  useEffect(() => {
    void loadMetadata();
  }, [loadMetadata]);

  return (
    <div className={styles.page}>
      <Title>
        <Translation
          translationKey={Localization.Admin.LiveStreaming.title}
          defaultText="Live Streaming"
        />
      </Title>
      <Paragraph type="secondary">
        <Translation
          translationKey={Localization.Admin.LiveStreaming.description}
          defaultText="Configure SRT ingest and WebRTC delivery alongside the existing RTMP/HLS pipeline. New protocols are disabled by default and do not change existing stream behavior."
        />
      </Paragraph>

      {metadataError && (
        <Alert
          type="error"
          showIcon
          message={metadataError}
          description="The settings form needs the server's settings metadata. The Live Stream Status tab still works."
          action={
            <Button size="small" onClick={() => void loadMetadata()}>
              <Translation
                translationKey={Localization.Admin.LiveStreaming.Settings.retry}
                defaultText="Retry"
              />
            </Button>
          }
        />
      )}

      <Tabs
        activeKey={activeTab}
        onChange={setActiveTab}
        items={[
          {
            key: 'srt',
            label: (
              <Translation
                translationKey={Localization.Admin.LiveStreaming.TabSrt}
                defaultText="SRT Configuration"
              />
            ),
            children: <SrtConfigSection metadata={metadata} />,
          },
          {
            key: 'webrtc',
            label: (
              <Translation
                translationKey={Localization.Admin.LiveStreaming.TabWebRtc}
                defaultText="WebRTC Configuration"
              />
            ),
            children: <WebRtcConfigSection metadata={metadata} />,
          },
          {
            key: 'status',
            label: (
              <Translation
                translationKey={Localization.Admin.LiveStreaming.TabStatus}
                defaultText="Live Stream Status"
              />
            ),
            children: <LiveStreamStatusSection />,
          },
        ]}
      />
    </div>
  );
}

ConfigLiveStreaming.getLayout = function getLayout(page: ReactElement) {
  return <AdminLayout page={page} />;
};
