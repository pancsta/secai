function scrollMsgs() {
    const m = document.getElementById("msgs")
    m.scrollTop = m.scrollHeight
}

function isMsgsScrolledEnd() {
    const m = document.getElementById("msgs")
    return m.scrollTop + m.offsetHeight >= m.scrollHeight*0.95
}

function clockmojiUpdate(newDiff) {     
    // console.log(newDiff)
    // newDiff = [
    //     [1,1,1,1,5,1,1,1,1,1,1],
    //     [3,1,1,1,1,1,1,1,1,1,1],
    //     [1,1,1,1,1,1,1,1,3,1,1],
    // ]

    maxVal = 0
    window.clockmoji.data.datasets.forEach((dataset, i) => {
        dataset.data.length = 0
        newDiff[i].forEach((v) => {
            // TODO handle in go
            val = v+2
            dataset.data.push(val);
            if (val > maxVal) {
                maxVal = val
            }
        })
    });
    window.clockmoji.options.scales.y.max = maxVal
    window.clockmoji.update("none")
}

function clockmojiInit() {
    const ctx = document.getElementById('clockmoji').getContext('2d');
    const Utils = ChartUtils.init();

    window.clockmoji = new Chart(ctx, {
        type: 'radar',
        data: {
            labels: Array.from({length: 15}, (_, i) => i),
            datasets: [{
                data: [],
                borderColor: '#4dc9f6',
                backgroundColor: Utils.transparentize('#4dc9f6', 0.5),
                borderWidth: 3,
                borderRadius: Number.MAX_VALUE,
                lineTension: 0.4,
                pointRadius: 0,
            }, {
                data: [],
                borderColor: '#f67019',
                backgroundColor: Utils.transparentize('#f67019', 0.5),
                borderWidth: 3,
                borderRadius: Number.MAX_VALUE,
                lineTension: 0.4,
                pointRadius: 0,
            }, {
                data: [],
                borderColor: '#acc236',
                backgroundColor: Utils.transparentize('#acc236', 0.5),
                borderWidth: 3,
                borderRadius: Number.MAX_VALUE,
                lineTension: 0.4,
                pointRadius: 0,
            }, {
                data: [],
                borderColor: 'red',
                backgroundColor: Utils.transparentize('red', 0.5),
                borderWidth: 3,
                borderRadius: Number.MAX_VALUE,
                lineTension: 0.4,
                pointRadius: 0,
            }]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            plugins: {
                legend: {display: false},
                tooltip: {enabled: false}
            },
            scales: {
                x: {display: false},
                y: {
                    display: false,
                    min: 0,
                    max: 16
                },
                r: {
                    pointLabels: {display: false},
                    ticks: {display: false},
                    angleLines: {display: false},
                    grid: {display: false},
                }
            },
            layout: {
                padding: 0
            }
        }
    });
}