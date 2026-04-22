/**
 * WebGL views: (1) libp2p peers in 3D, (2) Phase-3 parent mesh reference from /api/mesh3d-viz.
 * Requires import maps in index.html for three + addons.
 */
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';

const fetchOpts = { credentials: 'include', headers: { Accept: 'application/json' } };

function fibonacciSpherePoints(n, radius) {
    const pts = [];
    const golden = Math.PI * (3 - Math.sqrt(5));
    for (let i = 0; i < n; i++) {
        const y = 1 - (2 * i) / Math.max(n - 1, 1);
        const rr = Math.sqrt(Math.max(0, 1 - y * y));
        const theta = golden * i;
        pts.push(new THREE.Vector3(Math.cos(theta) * rr * radius, y * radius, Math.sin(theta) * rr * radius));
    }
    return pts;
}

function makeSceneBackground() {
    const scene = new THREE.Scene();
    scene.background = new THREE.Color(0x0f1419);
    const amb = new THREE.AmbientLight(0x606080, 0.9);
    scene.add(amb);
    const dir = new THREE.DirectionalLight(0xffffff, 0.55);
    dir.position.set(40, 80, 60);
    scene.add(dir);
    return scene;
}

function createViz(container, labelHint) {
    const w = container.clientWidth || 600;
    const h = container.clientHeight || 420;
    const scene = makeSceneBackground();
    const camera = new THREE.PerspectiveCamera(55, w / h, 0.1, 2000);
    camera.position.set(0, 120, 220);
    const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: false });
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
    renderer.setSize(w, h);
    container.innerHTML = '';
    container.appendChild(renderer.domElement);
    const controls = new OrbitControls(camera, renderer.domElement);
    controls.enableDamping = true;
    controls.dampingFactor = 0.06;
    const root = new THREE.Group();
    scene.add(root);
    const hint = document.createElement('div');
    hint.style.cssText = 'position:absolute;bottom:8px;left:10px;color:#666;font-size:11px;pointer-events:none;';
    hint.textContent = labelHint;
    container.style.position = 'relative';
    container.appendChild(hint);
    return { scene, camera, renderer, controls, root, hint };
}

function resizeViz(viz, container) {
    if (!viz || !container) return;
    const w = container.clientWidth;
    const h = container.clientHeight || 420;
    if (w < 2 || h < 2) return;
    viz.camera.aspect = w / h;
    viz.camera.updateProjectionMatrix();
    viz.renderer.setSize(w, h);
}

function clearGroup(g) {
    while (g.children.length) {
        const o = g.children[0];
        g.remove(o);
        if (o.geometry) o.geometry.dispose();
        if (o.material) {
            if (Array.isArray(o.material)) o.material.forEach((m) => m.dispose());
            else o.material.dispose();
        }
    }
}

function sphereAt(pos, color, radius, label) {
    const geom = new THREE.SphereGeometry(radius, 24, 24);
    const mat = new THREE.MeshStandardMaterial({ color, metalness: 0.2, roughness: 0.65 });
    const mesh = new THREE.Mesh(geom, mat);
    mesh.position.copy(pos);
    mesh.userData.label = label;
    return mesh;
}

function lineBetween(a, b, color, opacity) {
    const geom = new THREE.BufferGeometry().setFromPoints([a.clone(), b.clone()]);
    const mat = new THREE.LineBasicMaterial({ color, transparent: true, opacity });
    return new THREE.Line(geom, mat);
}

function updateP2P3D(viz, data) {
    if (!viz || !data || data.error) return;
    const g = viz.root;
    clearGroup(g);
    const nodes = data.nodes || [];
    const edges = data.edges || [];
    const selfNode = nodes.find((n) => n.type === 'self');
    if (!selfNode) return;
    const origin = new THREE.Vector3(0, 0, 0);
    g.add(sphereAt(origin, 0x4a9eff, 14, selfNode.label || 'Self'));
    const peers = nodes.filter((n) => n.type !== 'self');
    const positions = fibonacciSpherePoints(Math.max(peers.length, 1), 95);
    const posById = {};
    posById[selfNode.id] = origin;
    peers.forEach((node, i) => {
        const col = node.type === 'peer' ? 0x7ed321 : 0xf5a623;
        const p = positions[i] || new THREE.Vector3(60, 0, 0);
        posById[node.id] = p;
        g.add(sphereAt(p, col, 9, node.label || node.id));
    });
    edges.forEach((e) => {
        const from = posById[e.from];
        const to = posById[e.to];
        if (!from || !to) return;
        const c = e.status === 'connected' ? 0x5a9c2a : 0xc98a20;
        g.add(lineBetween(from, to, c, 0.45));
    });
}

function updateMesh3DRef(viz, data) {
    if (!viz || !data || !data.cells) return;
    const g = viz.root;
    clearGroup(g);
    const byId = {};
    (data.cells || []).forEach((c) => {
        const pos = new THREE.Vector3(Number(c.x) || 0, Number(c.y) || 0, Number(c.z) || 0);
        let col = 0x888888;
        let r = 10;
        if (c.role === 'vertex') {
            col = 0x6bb3ff;
            r = 13;
        } else if (c.role === 'parent') {
            col = 0x6abf4f;
            r = 10;
        }
        const mesh = sphereAt(pos, col, r, c.label);
        byId[c.id] = pos;
        g.add(mesh);
    });
    (data.links || []).forEach((ln) => {
        const a = byId[ln.from];
        const b = byId[ln.to];
        if (!a || !b) return;
        const col = ln.kind === 'dependency' ? 0x4a9eff : 0x556677;
        const op = ln.kind === 'dependency' ? 0.55 : 0.28;
        g.add(lineBetween(a, b, col, op));
    });
}

let p2pViz = null;
let meshViz = null;
let p2pContainer = null;
let meshContainer = null;

function animate() {
    requestAnimationFrame(animate);
    if (p2pViz) {
        p2pViz.controls.update();
        p2pViz.renderer.render(p2pViz.scene, p2pViz.camera);
    }
    if (meshViz) {
        meshViz.controls.update();
        meshViz.renderer.render(meshViz.scene, meshViz.camera);
    }
}

async function refreshP2P() {
    const el = document.getElementById('p2p-3d-error');
    try {
        const res = await fetch('/api/topology', fetchOpts);
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || res.statusText);
        updateP2P3D(p2pViz, data);
        if (el) {
            el.style.display = 'none';
        }
    } catch (e) {
        console.warn('3D topology:', e);
        if (el) {
            el.style.display = 'block';
            el.textContent = '3D P2P: ' + e.message;
        }
    }
}

async function refreshMesh3D() {
    const el = document.getElementById('mesh3d-3d-error');
    try {
        const res = await fetch('/api/mesh3d-viz', fetchOpts);
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || res.statusText);
        updateMesh3DRef(meshViz, data);
        const cap = document.getElementById('mesh3d-3d-caption');
        if (cap && data.title) {
            cap.textContent = data.description || data.title;
        }
        if (el) el.style.display = 'none';
    } catch (e) {
        console.warn('mesh3d viz:', e);
        if (el) {
            el.style.display = 'block';
            el.textContent = '3D mesh: ' + e.message;
        }
    }
}

function init() {
    p2pContainer = document.getElementById('p2p-3d-container');
    meshContainer = document.getElementById('mesh3d-3d-container');
    if (p2pContainer) {
        p2pViz = createViz(p2pContainer, 'Drag to orbit · scroll to zoom · live peers from /api/topology');
        new ResizeObserver(() => resizeViz(p2pViz, p2pContainer)).observe(p2pContainer);
    }
    if (meshContainer) {
        meshViz = createViz(meshContainer, 'Reference geometry from /api/mesh3d-viz');
        new ResizeObserver(() => resizeViz(meshViz, meshContainer)).observe(meshContainer);
    }
    animate();
    refreshP2P();
    refreshMesh3D();
    setInterval(() => {
        refreshP2P();
        refreshMesh3D();
    }, 4000);
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}
