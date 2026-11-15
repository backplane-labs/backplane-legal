import 'virtual:backplane-grafana-assets';
import { BackplaneClient } from '@backplane-labs/backplane-client';
import { editorsEnglish } from '@backplane-labs/backplane-editors';
import { platformEnglish } from '@backplane-labs/backplane-platform-ui';
import { installPluginTranslations, PluginProvider } from '@backplane-labs/backplane-plugin-sdk';
import { BackplaneProvider } from '@backplane-labs/backplane-react';
import { createGrafanaTheme, type ThemeMode } from '@backplane-labs/backplane-theme';
import { Badge, Button, englishResources, Input, Panel, Stack } from '@backplane-labs/backplane-ui';
import { ThemeContext } from '@grafana/ui';
import i18next from 'i18next';
import { useEffect, useLayoutEffect, useState, useSyncExternalStore } from 'react';
import { createRoot } from 'react-dom/client';
import { I18nextProvider, initReactI18next, useTranslation } from 'react-i18next';
import { BrowserRouter, NavLink, Route, Routes } from 'react-router-dom';
import Embedded from './embedded';
import navigation from './navigation';
import '@backplane-labs/backplane-ui/style.css';
import '@backplane-labs/backplane-theme/style.css';
import '@fontsource/ibm-plex-sans/400.css';
import '@fontsource/ibm-plex-sans/500.css';
import '@fontsource/ibm-plex-sans/600.css';
import '@fontsource/ibm-plex-mono/400.css';
import './standalone.css';

const i18n = i18next.createInstance();
await i18n.use(initReactI18next).init({
  lng: 'en',
  fallbackLng: 'en',
  supportedLngs: ['en'],
  interpolation: { escapeValue: false },
  resources: {
    en: {
      'backplane.ui': englishResources,
      'backplane.editors': editorsEnglish,
      'backplane.platform': platformEnglish,
    },
  },
});
installPluginTranslations(i18n, navigation);
const client = new BackplaneClient();
function Session() {
  const state = useSyncExternalStore(client.subscribe, client.getSnapshot),
    [token, setToken] = useState(''),
    [pending, setPending] = useState(false),
    [error, setError] = useState<Error>();
  useEffect(() => {
    void client.start().catch((reason) => setError(reason instanceof Error ? reason : new Error(String(reason))));
    return () => client.dispose();
  }, []);
  if (state.session)
    return (
      <Routes>
        <Route path="/*" element={<Embedded />} />
      </Routes>
    );
  return (
    <Panel title="Connect to Backplane">
      <Stack gap={12}>
        <p>
          This standalone shell uses the authenticated Backplane console API on the same origin. Serve it behind the
          console API gateway; no sample evidence or simulated mutations are used.
        </p>
        <p role="status">Connection: {state.connection}</p>
        {(error ?? state.error) != null && <p role="alert">{String(error ?? state.error)}</p>}
        <form
          onSubmit={(event) => {
            event.preventDefault();
            setPending(true);
            setError(undefined);
            void client
              .login(token)
              .then(() => setToken(''))
              .catch((reason) => setError(reason instanceof Error ? reason : new Error(String(reason))))
              .finally(() => setPending(false));
          }}
        >
          <Stack gap={8}>
            <label htmlFor="legal-token">Console access token</label>
            <Input
              id="legal-token"
              type="password"
              autoComplete="off"
              value={token}
              disabled={pending}
              onChange={(event) => setToken(event.target.value)}
            />
            <Button type="submit" disabled={pending || !token.trim()}>
              {pending ? 'Connecting…' : 'Sign in'}
            </Button>
          </Stack>
        </form>
      </Stack>
    </Panel>
  );
}
function Standalone() {
  const { t } = useTranslation('module.legal');
  const [mode, setMode] = useState<ThemeMode>('dark');
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = mode;
    document.documentElement.classList.toggle('dark', mode === 'dark');
  }, [mode]);
  return (
    <ThemeContext.Provider value={createGrafanaTheme(mode)}>
      <BackplaneProvider client={client}>
        <PluginProvider context={{ service: 'legal', basePath: '/', mode, environment: 'standalone' }}>
          <header>
            <span className="module-brand">
              {navigation.service}
              <Badge variant="outline">{t('standalone')}</Badge>
            </span>
            <nav>
              {navigation.nav.map((item) => (
                <NavLink key={item.path} end to={`/${item.path}`}>
                  {t(item.labelKey)}
                </NavLink>
              ))}
            </nav>
            <Button variant="outline" size="sm" onClick={() => setMode(mode === 'dark' ? 'light' : 'dark')}>
              {t(mode === 'dark' ? 'light' : 'dark')}
            </Button>
          </header>
          <main>
            <Session />
          </main>
        </PluginProvider>
      </BackplaneProvider>
    </ThemeContext.Provider>
  );
}
const container = document.getElementById('root');
if (container === null) throw new Error('standalone: #root element not found');
createRoot(container).render(
  <I18nextProvider i18n={i18n}>
    <BrowserRouter>
      <Standalone />
    </BrowserRouter>
  </I18nextProvider>,
);
