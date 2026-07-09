import { mount } from 'svelte';
import App from './App.svelte';
import './app.css';
import { setupI18n } from '$lib/i18n';

setupI18n();
mount(App, { target: document.getElementById('app')! });
