package main

const backofficeHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Conciliation Backoffice</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; background: #0f172a; color: #e2e8f0; padding: 20px; }
  h1 { font-size: 1.5rem; margin-bottom: 20px; color: #f8fafc; }
  h2 { font-size: 1.1rem; margin-bottom: 12px; color: #94a3b8; }

  .toolbar { display: flex; gap: 10px; margin-bottom: 20px; align-items: center; }
  .toolbar button { padding: 8px 16px; border: none; border-radius: 6px; cursor: pointer; font-size: 0.85rem; font-weight: 500; }
  .btn-primary { background: #3b82f6; color: white; }
  .btn-primary:hover { background: #2563eb; }
  .btn-danger { background: #ef4444; color: white; }
  .btn-danger:hover { background: #dc2626; }
  .btn-sm { padding: 4px 10px; font-size: 0.75rem; border-radius: 4px; border: none; cursor: pointer; }
  .btn-edit { background: #f59e0b; color: #1e293b; }
  .btn-edit:hover { background: #d97706; }
  .btn-run { background: #10b981; color: white; }
  .btn-run:hover { background: #059669; }

  .status-msg { padding: 8px 14px; border-radius: 6px; font-size: 0.85rem; display: none; }
  .status-msg.show { display: inline-block; }
  .status-msg.ok { background: #166534; color: #bbf7d0; }
  .status-msg.err { background: #7f1d1d; color: #fecaca; }

  .partners-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); gap: 14px; margin-bottom: 24px; }
  .partner-card {
    background: #1e293b; border-radius: 8px; padding: 16px; cursor: pointer;
    border: 2px solid transparent; transition: border-color 0.15s;
  }
  .partner-card:hover { border-color: #3b82f6; }
  .partner-card.selected { border-color: #3b82f6; }
  .partner-card .name { font-size: 1rem; font-weight: 600; color: #f1f5f9; }
  .partner-card .code { font-size: 0.8rem; color: #64748b; margin-bottom: 8px; }
  .partner-card .stats { display: grid; grid-template-columns: 1fr 1fr; gap: 6px; font-size: 0.8rem; }
  .partner-card .stat { display: flex; justify-content: space-between; }
  .partner-card .stat .label { color: #94a3b8; }
  .partner-card .stat .value { font-weight: 600; }
  .tier-badge { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 0.7rem; font-weight: 600; text-transform: uppercase; margin-left: 8px; }
  .tier-light { background: #166534; color: #bbf7d0; }
  .tier-medium { background: #854d0e; color: #fef08a; }
  .tier-heavy { background: #9a3412; color: #fed7aa; }
  .tier-extra-heavy { background: #7f1d1d; color: #fecaca; }

  .progress-bar { height: 4px; background: #334155; border-radius: 2px; margin-top: 8px; overflow: hidden; }
  .progress-bar .fill { height: 100%; border-radius: 2px; transition: width 0.3s; }
  .fill-green { background: #10b981; }
  .fill-blue { background: #3b82f6; }

  .merchants-section { background: #1e293b; border-radius: 8px; padding: 20px; }
  table { width: 100%; border-collapse: collapse; font-size: 0.82rem; }
  th { text-align: left; color: #64748b; font-weight: 500; padding: 8px 10px; border-bottom: 1px solid #334155; }
  td { padding: 8px 10px; border-bottom: 1px solid #1e293b; }
  tr:hover td { background: #0f172a; }
  .loan-active { color: #fbbf24; }
  .loan-paid { color: #10b981; }
  .no-loan { color: #475569; font-style: italic; }

  .modal-overlay {
    display: none; position: fixed; inset: 0; background: rgba(0,0,0,0.6); z-index: 100;
    align-items: center; justify-content: center;
  }
  .modal-overlay.show { display: flex; }
  .modal {
    background: #1e293b; border-radius: 10px; padding: 24px; width: 400px; max-width: 90vw;
    border: 1px solid #334155;
  }
  .modal h3 { margin-bottom: 16px; font-size: 1rem; }
  .modal label { display: block; font-size: 0.8rem; color: #94a3b8; margin-bottom: 4px; }
  .modal input, .modal select { width: 100%; padding: 8px 10px; border-radius: 6px; border: 1px solid #334155; background: #0f172a; color: #e2e8f0; margin-bottom: 12px; font-size: 0.85rem; }
  .modal .actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 8px; }
  .modal .btn-cancel { background: #334155; color: #94a3b8; padding: 8px 16px; border: none; border-radius: 6px; cursor: pointer; }
  .spin { animation: spin 1s linear infinite; display: inline-block; }
  @keyframes spin { to { transform: rotate(360deg); } }

  .number { font-variant-numeric: tabular-nums; }
  .amount { color: #34d399; }
  .amount-zero { color: #475569; }
</style>
</head>
<body>

<h1>Conciliation Backoffice</h1>

<div class="toolbar">
  <button class="btn-primary" onclick="loadPartners()">Refresh</button>
  <button class="btn-danger" onclick="resetDB()">Reset Database</button>
  <span id="statusMsg" class="status-msg"></span>
</div>

<div id="partnersGrid" class="partners-grid"></div>

<div id="merchantsSection" class="merchants-section" style="display:none">
  <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:12px">
    <h2 id="merchantsTitle">Merchants</h2>
    <button class="btn-sm btn-run" onclick="openCreateTxnModal()">+ Create Transactions</button>
  </div>
  <table>
    <thead>
      <tr>
        <th>ID</th>
        <th>Name</th>
        <th>Loan</th>
        <th>Original</th>
        <th>Remaining</th>
        <th>Progress</th>
        <th>Pending</th>
        <th>Processed</th>
        <th>Skipped</th>
        <th>Collections</th>
        <th>Collected</th>
        <th></th>
      </tr>
    </thead>
    <tbody id="merchantsBody"></tbody>
  </table>
</div>

<!-- Edit Loan Modal -->
<div id="loanModal" class="modal-overlay">
  <div class="modal">
    <h3>Edit Loan</h3>
    <label>Loan ID</label>
    <input id="loanId" disabled>
    <label>Current Remaining</label>
    <input id="loanCurrent" disabled>
    <label>New Remaining Amount</label>
    <input id="loanNewAmount" type="number" step="0.01" min="0">
    <div class="actions">
      <button class="btn-cancel" onclick="closeLoanModal()">Cancel</button>
      <button class="btn-primary" onclick="saveLoan()">Save</button>
    </div>
  </div>
</div>

<!-- Create Transactions Modal -->
<div id="txnModal" class="modal-overlay">
  <div class="modal">
    <h3>Create Transactions</h3>
    <label>Merchant</label>
    <select id="txnMerchant"></select>
    <label>Number of Transactions</label>
    <input id="txnCount" type="number" value="1000" min="1" max="100000">
    <label>Amount per Transaction (USD)</label>
    <input id="txnAmount" type="number" value="10000" step="0.01" min="0.01">
    <div class="actions">
      <button class="btn-cancel" onclick="closeTxnModal()">Cancel</button>
      <button class="btn-primary" onclick="createTransactions()">Create</button>
    </div>
  </div>
</div>

<script>
let partners = [];
let currentPartner = null;
let currentMerchants = [];

function showStatus(msg, ok) {
  const el = document.getElementById('statusMsg');
  el.textContent = msg;
  el.className = 'status-msg show ' + (ok ? 'ok' : 'err');
  setTimeout(() => el.className = 'status-msg', 3000);
}

function fmt(n) {
  return new Intl.NumberFormat('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(n);
}

function fmtInt(n) {
  return new Intl.NumberFormat('en-US').format(n);
}

async function loadPartners() {
  try {
    const res = await fetch('/api/v1/partners');
    partners = await res.json();
    renderPartners();
    if (currentPartner) {
      loadMerchants(currentPartner);
    }
  } catch(e) {
    showStatus('Failed to load partners: ' + e.message, false);
  }
}

function tierClass(tier) {
  return 'tier-' + tier.replace('_', '-');
}

function renderPartners() {
  const grid = document.getElementById('partnersGrid');
  if (!partners || partners.length === 0) {
    grid.innerHTML = '<p style="color:#64748b">No partners found</p>';
    return;
  }
  grid.innerHTML = partners.map(p => {
    const totalTxns = p.pending_txns + p.processed_txns + p.skipped_txns;
    const processedPct = totalTxns > 0 ? ((p.processed_txns + p.skipped_txns) / totalTxns * 100) : 0;
    const selected = currentPartner === p.id ? ' selected' : '';
    return '<div class="partner-card' + selected + '" onclick="selectPartner(' + p.id + ')">' +
      '<div class="name">' + p.name + '<span class="tier-badge ' + tierClass(p.tier) + '">' + p.tier + '</span></div>' +
      '<div class="code">' + p.code + '</div>' +
      '<div class="stats">' +
        '<div class="stat"><span class="label">Merchants</span><span class="value">' + fmtInt(p.merchant_count) + '</span></div>' +
        '<div class="stat"><span class="label">Active Loans</span><span class="value">' + fmtInt(p.active_loans) + '</span></div>' +
        '<div class="stat"><span class="label">Paid Loans</span><span class="value">' + fmtInt(p.paid_loans) + '</span></div>' +
        '<div class="stat"><span class="label">Pending Txns</span><span class="value">' + fmtInt(p.pending_txns) + '</span></div>' +
        '<div class="stat"><span class="label">Processed</span><span class="value">' + fmtInt(p.processed_txns) + '</span></div>' +
        '<div class="stat"><span class="label">Collections</span><span class="value">' + fmtInt(p.total_collections) + '</span></div>' +
        '<div class="stat"><span class="label">Collected</span><span class="value amount">$' + fmt(p.collected_amount) + '</span></div>' +
      '</div>' +
      '<div class="progress-bar"><div class="fill fill-green" style="width:' + processedPct.toFixed(1) + '%"></div></div>' +
    '</div>';
  }).join('');
}

function selectPartner(id) {
  currentPartner = id;
  renderPartners();
  loadMerchants(id);
}

async function loadMerchants(partnerId) {
  try {
    const res = await fetch('/api/v1/partners/' + partnerId + '/merchants');
    currentMerchants = await res.json();
    renderMerchants();
  } catch(e) {
    showStatus('Failed to load merchants: ' + e.message, false);
  }
}

function renderMerchants() {
  const section = document.getElementById('merchantsSection');
  const p = partners.find(x => x.id === currentPartner);
  document.getElementById('merchantsTitle').textContent = p ? p.name + ' - Merchants' : 'Merchants';
  section.style.display = 'block';

  const body = document.getElementById('merchantsBody');
  if (!currentMerchants || currentMerchants.length === 0) {
    body.innerHTML = '<tr><td colspan="12" style="color:#64748b;text-align:center">No merchants</td></tr>';
    return;
  }
  body.innerHTML = currentMerchants.map(m => {
    let loanCell, origCell, remCell, progressCell, editCell;
    if (m.has_loan) {
      const cls = m.loan_status === 'paid' ? 'loan-paid' : 'loan-active';
      loanCell = '<span class="' + cls + '">' + m.loan_status + '</span>';
      origCell = '$' + fmt(m.original_amount);
      remCell = m.remaining_amount > 0 ? '<span class="amount">$' + fmt(m.remaining_amount) + '</span>' : '<span class="amount-zero">$0.00</span>';
      const pct = m.original_amount > 0 ? ((m.original_amount - m.remaining_amount) / m.original_amount * 100) : 100;
      progressCell = '<div class="progress-bar" style="width:80px;display:inline-block"><div class="fill fill-blue" style="width:' + pct.toFixed(1) + '%"></div></div> <span style="font-size:0.7rem;color:#64748b">' + pct.toFixed(0) + '%</span>';
      editCell = '<button class="btn-sm btn-edit" onclick="openLoanModal(' + m.loan_id + ',' + m.remaining_amount + ')">Edit</button>';
    } else {
      loanCell = '<span class="no-loan">none</span>';
      origCell = '-';
      remCell = '-';
      progressCell = '-';
      editCell = '';
    }
    return '<tr>' +
      '<td class="number">' + m.id + '</td>' +
      '<td>' + m.name + '</td>' +
      '<td>' + loanCell + '</td>' +
      '<td class="number">' + origCell + '</td>' +
      '<td class="number">' + remCell + '</td>' +
      '<td>' + progressCell + '</td>' +
      '<td class="number">' + fmtInt(m.pending_txns) + '</td>' +
      '<td class="number">' + fmtInt(m.processed_txns) + '</td>' +
      '<td class="number">' + fmtInt(m.skipped_txns) + '</td>' +
      '<td class="number">' + fmtInt(m.collections) + '</td>' +
      '<td class="number amount">' + (m.collected_amount > 0 ? '$' + fmt(m.collected_amount) : '<span class="amount-zero">$0.00</span>') + '</td>' +
      '<td>' + editCell + '</td>' +
    '</tr>';
  }).join('');
}

function openLoanModal(loanId, currentRemaining) {
  document.getElementById('loanId').value = loanId;
  document.getElementById('loanCurrent').value = '$' + fmt(currentRemaining);
  document.getElementById('loanNewAmount').value = currentRemaining;
  document.getElementById('loanModal').classList.add('show');
}

function closeLoanModal() {
  document.getElementById('loanModal').classList.remove('show');
}

async function saveLoan() {
  const loanId = document.getElementById('loanId').value;
  const amount = parseFloat(document.getElementById('loanNewAmount').value);
  try {
    const res = await fetch('/api/v1/loans/' + loanId, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ remaining_amount: amount })
    });
    if (!res.ok) throw new Error(await res.text());
    closeLoanModal();
    showStatus('Loan updated', true);
    loadMerchants(currentPartner);
    loadPartners();
  } catch(e) {
    showStatus('Failed: ' + e.message, false);
  }
}

function openCreateTxnModal() {
  const sel = document.getElementById('txnMerchant');
  sel.innerHTML = (currentMerchants || []).map(m =>
    '<option value="' + m.id + '">' + m.name + ' (' + m.external_id + ')</option>'
  ).join('');
  document.getElementById('txnModal').classList.add('show');
}

function closeTxnModal() {
  document.getElementById('txnModal').classList.remove('show');
}

async function createTransactions() {
  const merchantId = parseInt(document.getElementById('txnMerchant').value);
  const count = parseInt(document.getElementById('txnCount').value);
  const amount = parseFloat(document.getElementById('txnAmount').value);
  try {
    const res = await fetch('/api/v1/transactions/create', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ merchant_id: merchantId, count: count, amount: amount })
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error);
    closeTxnModal();
    showStatus(data.message, true);
    loadMerchants(currentPartner);
    loadPartners();
  } catch(e) {
    showStatus('Failed: ' + e.message, false);
  }
}

async function resetDB() {
  if (!confirm('Reset all data? Collections will be deleted, transactions set to pending, loans reset to original.')) return;
  try {
    const res = await fetch('/api/v1/reset', { method: 'POST' });
    if (!res.ok) throw new Error(await res.text());
    showStatus('Database reset complete', true);
    loadPartners();
  } catch(e) {
    showStatus('Reset failed: ' + e.message, false);
  }
}

// Auto-refresh
loadPartners();
setInterval(loadPartners, 10000);
</script>
</body>
</html>`
